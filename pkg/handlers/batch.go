package handlers

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kacperjurak/goimpcore/internal/utils"
	"github.com/kacperjurak/goimpcore/pkg/config"
	"github.com/kacperjurak/goimpcore/pkg/models"
	"github.com/kacperjurak/goimpcore/pkg/worker"
)

// BatchHandler handles batch EIS data processing requests
type BatchHandler struct {
	config     *config.Config
	workerPool *worker.Pool
	processor  ProcessorFunc
}

// NewBatchHandler creates a new batch handler
func NewBatchHandler(cfg *config.Config, pool *worker.Pool, processor ProcessorFunc) *BatchHandler {
	return &BatchHandler{
		config:     cfg,
		workerPool: pool,
		processor:  processor,
	}
}

// ServeHTTP implements the http.Handler interface
func (h *BatchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.setupCORS(w)

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "POST" {
		h.writeError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var batch models.ImpedanceBatch
	if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
		h.writeError(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	if len(batch.Spectra) == 0 {
		h.writeError(w, "No spectra provided in batch", http.StatusBadRequest)
		return
	}

	// Generate BatchID if not provided by client
	if batch.BatchID == nil || *batch.BatchID == "" {
		generatedID := utils.GenerateID()
		batch.BatchID = &generatedID
	}

	log.Printf("🔄 Batch processing started - ID: %s, Spectra: %d", *batch.BatchID, len(batch.Spectra))

	// Process batch asynchronously
	go h.processBatchAsync(batch)

	// Return immediate response
	response := map[string]interface{}{
		"success":  true,
		"batch_id": *batch.BatchID,
		"spectra":  len(batch.Spectra),
		"message":  "Batch processing started with worker pool",
	}

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(response)
}

// processBatchAsync handles asynchronous batch processing
func (h *BatchHandler) processBatchAsync(batch models.ImpedanceBatch) {
	batchStartTime := time.Now()
	eisMetricsDataResults := make([]models.EISMetricsDataResult, len(batch.Spectra))
	totalSpectra := len(batch.Spectra)
	concurrency := h.getConcurrency()

	log.Printf("🔄 Starting concurrent batch processing - %d spectra with %d workers", totalSpectra, concurrency)

	// Submit jobs in streaming fashion to avoid queue overflow
	// Start a goroutine to submit jobs while workers process them
	go func() {
		log.Printf("📦 Submitting %d jobs to worker pool", totalSpectra)
		for _, spectrum := range batch.Spectra {
			job := h.createWorkItem(spectrum, *batch.BatchID)
			h.workerPool.SubmitJob(job) // This will block if queue is full, providing natural backpressure
		}
		log.Printf("✅ All jobs submitted to worker pool")
	}()

	// Collect ALL results concurrently
	resultsReceived := h.collectAllResults(totalSpectra, eisMetricsDataResults)

	if resultsReceived < len(batch.Spectra) {
		log.Printf("⚠️ Missing results! Expected %d, received %d", len(batch.Spectra), resultsReceived)
	} else {
		log.Printf("✅ All %d results collected successfully", resultsReceived)
	}

	// All results processed and webhooks sent
	totalBatchTime := time.Since(batchStartTime)

	log.Printf("🔄 About to save timing results - BatchID: %s, Duration: %v, Concurrency: %d", *batch.BatchID, totalBatchTime, concurrency)

	// Save timing results to file
	h.saveTimingResults(*batch.BatchID, totalBatchTime, eisMetricsDataResults, concurrency)

	log.Printf("✅ CSV saving completed for batch: %s", *batch.BatchID)

	log.Printf("🎉 Batch processing completed - ID: %s, Total time: %v", *batch.BatchID, totalBatchTime)
}

// createWorkItem converts a batch item to a work item
func (h *BatchHandler) createWorkItem(item models.BatchItem, batchID string) models.WorkItem {
	// Convert to internal format with optimized data transformation
	freqs := item.ImpedanceData.Frequencies
	impData := make([][2]float64, len(item.ImpedanceData.Impedance))

	log.Printf("DEBUG: Processing spectrum %d with %d frequencies and %d impedance points",
		item.Iteration, len(freqs), len(item.ImpedanceData.Impedance))

	// Optimized data conversion - single pass
	for i, point := range item.ImpedanceData.Impedance {
		realVal, realOk := point["real"]
		imagVal, imagOk := point["imag"]

		if !realOk || !imagOk {
			log.Printf("ERROR: Invalid impedance point format at index %d: %+v", i, point)
			continue
		}

		if math.IsNaN(realVal) || math.IsInf(realVal, 0) || math.IsNaN(imagVal) || math.IsInf(imagVal, 0) {
			log.Printf("WARNING: Invalid impedance values at index %d: real=%v, imag=%v", i, realVal, imagVal)
		}

		impData[i] = [2]float64{realVal, imagVal}
	}

	return models.WorkItem{
		ID:        item.Iteration,
		RequestID: utils.GenerateID(),
		BatchID:   batchID,
		Iteration: item.Iteration,
		Freqs:     freqs,
		ImpData:   impData,
		Config:    h.config,
		StartTime: time.Now(), // Server-generated timestamp for accurate processing timing
	}
}

// processResultWithImmediateWebhook processes a work result and sends webhook immediately
func (h *BatchHandler) processResultWithImmediateWebhook(result models.WorkResult, eisMetricsDataResults []models.EISMetricsDataResult) {
	// Use iteration as 0-based index directly
	index := result.Iteration
	if index < 0 || index >= len(eisMetricsDataResults) {
		log.Printf("ERROR: Invalid iteration index %d (iteration=%d, array_length=%d)", index, result.Iteration, len(eisMetricsDataResults))
		return
	}
	eisMetricsDataResults[index] = models.EISMetricsDataResult{
		Iteration:          result.Iteration,
		ProcessingTime:     result.ProcessingTime,
		ChiSquare:          result.Result.Min, // Extract chi-square from EIS result
		Success:            result.Success,
		CircuitCode:        result.CircuitCode,
		OptimizationMethod: result.OptimizationMethod,
		Parameters:         result.Parameters, // Include fitted parameters
	}

	// Create webhook item with complete parameter information
	webhook := models.WebhookItem{
		RequestID:         fmt.Sprintf("%s_iter_%03d", result.RequestID, result.Iteration),
		ChiSquare:         result.Result.Min, // Extract chi-square from EIS result
		RealImp:           result.RealImp,
		ImagImp:           result.ImagImp,
		Freqs:             result.Freqs,
		Params:            result.Result.Params,                  // ✅ Add fitted parameters
		Elements:          h.getElementNames(result.CircuitCode), // ✅ Add element names
		ElementImpedances: h.calculateElementImpedances(result),  // ✅ Add element impedances
		CircuitCode:       result.CircuitCode,
	}

	// 🔍 PERFORMANCE LOGGING: Time webhook queuing
	webhookQueueStart := time.Now()

	// Send webhook directly without queuing to avoid buffer overflow
	go func() {
		if err := h.workerPool.SendWebhookDirect(webhook); err != nil {
			log.Printf("Failed to send webhook for %s: %v", webhook.RequestID, err)
		}
	}()

	webhookQueueDuration := time.Since(webhookQueueStart)

	// 🔍 PERFORMANCE LOGGING: Track webhook queue performance, especially around bottleneck area
	if result.Iteration >= 1150 && result.Iteration <= 1170 || webhookQueueDuration > time.Millisecond {
		log.Printf("🔍 WEBHOOK QUEUE - Iteration %d: queue_time=%v", result.Iteration, webhookQueueDuration)
	}

	if !h.config.Quiet {
		log.Printf("✅ Processed spectrum iteration %d and queued webhook immediately (queue_time=%v)", result.Iteration, webhookQueueDuration)
	}
}

// processResult processes a work result and updates timing (legacy function kept for compatibility)
func (h *BatchHandler) processResult(result models.WorkResult, eisMetricsDataResults []models.EISMetricsDataResult) {
	// Use iteration as 0-based index directly
	index := result.Iteration
	if index < 0 || index >= len(eisMetricsDataResults) {
		log.Printf("ERROR: Invalid iteration index %d (iteration=%d, array_length=%d)", index, result.Iteration, len(eisMetricsDataResults))
		return
	}
	eisMetricsDataResults[index] = models.EISMetricsDataResult{
		Iteration:          result.Iteration,
		ProcessingTime:     result.ProcessingTime,
		ChiSquare:          result.Result.Min, // Extract chi-square from EIS result
		Success:            result.Success,
		CircuitCode:        result.CircuitCode,
		OptimizationMethod: result.OptimizationMethod,
		Parameters:         result.Parameters, // Include fitted parameters
	}

	// Debug: Log the first few values to understand the data mapping issue
	log.Printf("DEBUG WEBHOOK: Frequencies[0:3]: %v", result.Freqs[:min(3, len(result.Freqs))])
	log.Printf("DEBUG WEBHOOK: RealImp[0:3]: %v", result.RealImp[:min(3, len(result.RealImp))])
	log.Printf("DEBUG WEBHOOK: ImagImp[0:3]: %v", result.ImagImp[:min(3, len(result.ImagImp))])

	// Create webhook item with complete parameter information
	webhook := models.WebhookItem{
		RequestID:         fmt.Sprintf("%s_iter_%03d", result.RequestID, result.Iteration),
		ChiSquare:         result.Result.Min, // Extract chi-square from EIS result
		RealImp:           result.RealImp,
		ImagImp:           result.ImagImp,
		Freqs:             result.Freqs,
		Params:            result.Result.Params,                  // ✅ Add fitted parameters
		Elements:          h.getElementNames(result.CircuitCode), // ✅ Add element names
		ElementImpedances: h.calculateElementImpedances(result),  // ✅ Add element impedances
		CircuitCode:       result.CircuitCode,
	}

	// Send webhook directly to avoid queue overflow
	go func() {
		if err := h.workerPool.SendWebhookDirect(webhook); err != nil {
			log.Printf("Failed to send webhook for %s: %v", webhook.RequestID, err)
		}
	}()

	if !h.config.Quiet {
		log.Printf("✅ Processed spectrum iteration %d", result.Iteration)
	}
}

// getElementNames returns element names for a given circuit code
func (h *BatchHandler) getElementNames(circuitCode string) []string {
	switch strings.ToLower(circuitCode) {
	case "r(qr)", "R(QR)":
		return []string{"r", "qy", "qn", "r"}
	case "r(qr)(qr)", "R(QR)(QR)":
		return []string{"r", "qy", "qn", "r", "qy", "qn", "r"}
	case "r(cr)", "R(CR)":
		return []string{"r", "c", "r"}
	case "r(cr)(cr)", "R(CR)(CR)":
		return []string{"r", "c", "r", "c", "r"}
	case "r(q(r(qr)))", "R(Q(R(QR)))":
		return []string{"r", "qy", "qn", "r", "qy", "qn", "r"}
	case "r(q(r(q(r(qr)))))", "R(Q(R(Q(R(QR)))))":
		return []string{"r", "qy", "qn", "r", "qy", "qn", "r", "qy", "qn", "r"}
	case "r(rc(r(cr)))", "R(RC(R(CR)))":
		return []string{"r", "r", "c", "r", "c", "r"}
	default:
		// Generic fallback: assume R(QR) pattern
		log.Printf("Warning: Unknown circuit code '%s', using R(QR) element names", circuitCode)
		return []string{"r", "qy", "qn", "r"}
	}
}

// collectAllResults collects all results concurrently without chunking
func (h *BatchHandler) collectAllResults(expectedResults int, eisMetricsDataResults []models.EISMetricsDataResult) int {
	resultsReceived := 0
	maxWaitTime := time.Minute * 5 // 5 minutes total (scales with job count)
	timeout := time.Now().Add(maxWaitTime)

	log.Printf("🕐 Collecting %d results concurrently", expectedResults)

	for resultsReceived < expectedResults {
		if time.Now().After(timeout) {
			log.Printf("⚠️ Timeout! Received %d/%d results", resultsReceived, expectedResults)
			break
		}

		// Drain all available results in batches for efficiency
		resultsBatch := 0
		for resultsReceived < expectedResults {
			if result, ok := h.workerPool.GetResult(); ok {
				// Process result and send webhook immediately
				h.processResultWithImmediateWebhook(result, eisMetricsDataResults)
				resultsReceived++
				resultsBatch++
			} else {
				break // No more results available right now
			}
		}

		if resultsBatch > 0 {
			log.Printf("🚀 BATCH DRAIN: Processed %d results (%d/%d complete)",
				resultsBatch, resultsReceived, expectedResults)
		} else {
			// Brief sleep to avoid CPU spinning
			time.Sleep(5 * time.Millisecond)
		}
	}

	log.Printf("✅ Result collection completed: %d/%d results", resultsReceived, expectedResults)
	return resultsReceived
}

// calculateElementImpedances calculates impedance for each circuit element (simplified version)
func (h *BatchHandler) calculateElementImpedances(result models.WorkResult) []models.ElementImpedance {
	// For now, return empty array - full implementation would require complex impedance calculations
	// This matches the TODO comment that was in the original code
	// The webplot service can still function with empty element impedances
	return []models.ElementImpedance{}
}

// getConcurrency returns the current concurrency level
func (h *BatchHandler) getConcurrency() int {
	concurrency := 5
	if h.config != nil && h.config.Threads > 0 {
		concurrency = int(h.config.Threads)
	}
	return concurrency
}

// saveTimingResults saves timing data to a CSV file for performance analysis
func (h *BatchHandler) saveTimingResults(batchID string, totalTime time.Duration, eisMetricsDataResults []models.EISMetricsDataResult, concurrency int) {
	log.Printf("🗂️  Starting to save timing results to CSV...")

	filename := "concurrent_timing_results.csv"

	// Ensure we save to current working directory
	if wd, err := os.Getwd(); err == nil {
		filename = filepath.Join(wd, "concurrent_timing_results.csv")
		log.Printf("📁 Working directory: %s", wd)
		log.Printf("📄 Full CSV path: %s", filename)
	} else {
		log.Printf("⚠️  Could not get working directory: %v", err)
	}

	// Check if file exists to decide on header
	var writeHeader bool
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		writeHeader = true
	}

	// Open file for append
	log.Printf("🔓 Opening CSV file for writing...")
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("❌ Error opening timing file: %v", err)
		return
	}
	defer file.Close()
	log.Printf("✅ CSV file opened successfully")

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write header if new file
	if writeHeader {
		header := []string{
			"Timestamp",
			"BatchID",
			"TotalSpectra",
			"Concurrency",
			"TotalBatchTime_ms",
			"AvgSpectrumTime_ms",
			"MinSpectrumTime_ms",
			"MaxSpectrumTime_ms",
			"SuccessRate",
			"AvgChiSquare",
			"SpectraPerSecond",
			"EfficiencyScore",
			"CircuitCode",
			"OptimizationMethod",
		}
		if err := writer.Write(header); err != nil {
			log.Printf("Error writing timing header: %v", err)
			return
		}
	}

	// Calculate statistics
	var totalSpectrumTime time.Duration
	var minTime, maxTime time.Duration = time.Hour, 0
	var successful int
	var totalChiSq float64

	for _, timing := range eisMetricsDataResults {
		totalSpectrumTime += timing.ProcessingTime
		if timing.ProcessingTime < minTime {
			minTime = timing.ProcessingTime
		}
		if timing.ProcessingTime > maxTime {
			maxTime = timing.ProcessingTime
		}
		if timing.Success {
			successful++
			totalChiSq += timing.ChiSquare
		}
	}

	numSpectra := len(eisMetricsDataResults)
	avgSpectrumTime := totalSpectrumTime / time.Duration(numSpectra)
	successRate := float64(successful) / float64(numSpectra) * 100
	avgChiSq := 0.0
	if successful > 0 {
		avgChiSq = totalChiSq / float64(successful)
	}

	spectraPerSecond := float64(numSpectra) / totalTime.Seconds()

	// Efficiency score: how well we utilized the concurrency
	// Perfect efficiency = 1.0 (linear speedup), poor efficiency < 0.5
	theoreticalTime := avgSpectrumTime * time.Duration(numSpectra)
	efficiencyScore := theoreticalTime.Seconds() / totalTime.Seconds() / float64(concurrency)

	// Get circuit code and optimization method from first spectrum timing (should be consistent across all spectra)
	circuitCode := "Unknown"
	optimizationMethod := "Unknown"
	if len(eisMetricsDataResults) > 0 {
		circuitCode = eisMetricsDataResults[0].CircuitCode
		optimizationMethod = eisMetricsDataResults[0].OptimizationMethod
	}

	// Write timing record
	record := []string{
		time.Now().Format(time.RFC3339),
		batchID,
		fmt.Sprintf("%d", numSpectra),
		fmt.Sprintf("%d", concurrency),
		fmt.Sprintf("%.2f", float64(totalTime.Nanoseconds())/1000000.0),
		fmt.Sprintf("%.2f", float64(avgSpectrumTime.Nanoseconds())/1000000.0),
		fmt.Sprintf("%.2f", float64(minTime.Nanoseconds())/1000000.0),
		fmt.Sprintf("%.2f", float64(maxTime.Nanoseconds())/1000000.0),
		fmt.Sprintf("%.1f", successRate),
		fmt.Sprintf("%.6e", avgChiSq),
		fmt.Sprintf("%.2f", spectraPerSecond),
		fmt.Sprintf("%.3f", efficiencyScore),
		circuitCode,
		optimizationMethod,
	}

	log.Printf("📝 Writing timing record to CSV...")
	if err := writer.Write(record); err != nil {
		log.Printf("❌ Error writing timing record: %v", err)
		return
	}
	log.Printf("✅ Timing record written successfully")

	log.Printf("📊 Timing saved: %d spectra, %d goroutines, %.2f ms total, %.2f%% success, %.3f efficiency",
		numSpectra, concurrency, float64(totalTime.Nanoseconds())/1000000.0, successRate, efficiencyScore)

	// Also save detailed per-spectrum results
	log.Printf("🔄 Now saving detailed spectrum results...")
	h.saveDetailedResults(batchID, eisMetricsDataResults)
	log.Printf("✅ Detailed results saving completed")
}

// saveDetailedResults saves per-spectrum detailed results including circuit parameters
func (h *BatchHandler) saveDetailedResults(batchID string, eisMetricsDataResults []models.EISMetricsDataResult) {
	log.Printf("🗂️  Starting to save detailed results to CSV...")

	filename := "detailed_spectrum_results.csv"

	// Ensure we save to current working directory
	if wd, err := os.Getwd(); err == nil {
		filename = filepath.Join(wd, "detailed_spectrum_results.csv")
		log.Printf("📄 Detailed CSV path: %s", filename)
	} else {
		log.Printf("⚠️  Could not get working directory for detailed results: %v", err)
	}

	// Check if file exists to decide on header
	var writeHeader bool
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		writeHeader = true
	}

	// Open file for append
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Error opening detailed results file: %v", err)
		return
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write header if new file
	if writeHeader {
		header := []string{
			"Timestamp",
			"BatchID",
			"SpectrumIteration",
			"ProcessingTime_ms",
			"ChiSquare",
			"Success",
			"CircuitCode",
			"OptimizationMethod",
			"Param1",
			"Param2",
			"Param3",
			"Param4",
			"Param5",
			"Param6",
			"Param7",
			"Param8",
			"Param9",
			"Param10",
		}
		if err := writer.Write(header); err != nil {
			log.Printf("Error writing detailed results header: %v", err)
			return
		}
	}

	// Write each spectrum result
	timestamp := time.Now().Format(time.RFC3339)
	for _, timing := range eisMetricsDataResults {
		record := []string{
			timestamp,
			batchID,
			fmt.Sprintf("%d", timing.Iteration),
			fmt.Sprintf("%.2f", float64(timing.ProcessingTime.Nanoseconds())/1000000.0),
			fmt.Sprintf("%.6e", timing.ChiSquare),
			fmt.Sprintf("%t", timing.Success),
			timing.CircuitCode,
			timing.OptimizationMethod,
		}

		// Add up to 10 parameters (pad with empty strings if fewer)
		for i := 0; i < 10; i++ {
			if i < len(timing.Parameters) {
				record = append(record, fmt.Sprintf("%.6e", timing.Parameters[i]))
			} else {
				record = append(record, "")
			}
		}

		if err := writer.Write(record); err != nil {
			log.Printf("Error writing detailed spectrum record: %v", err)
			return
		}
	}

	log.Printf("📋 Detailed results saved: %d spectrum records to %s", len(eisMetricsDataResults), filename)
	log.Printf("✅ All CSV writing operations completed successfully")
}

// setupCORS sets up CORS headers
func (h *BatchHandler) setupCORS(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

// writeError writes an error response
func (h *BatchHandler) writeError(w http.ResponseWriter, message string, statusCode int) {
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
