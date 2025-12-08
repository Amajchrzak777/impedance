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

	// Process batch asynchronously
	go h.processBatchAsync(batch)

	response := map[string]interface{}{
		"success":  true,
		"batch_id": *batch.BatchID,
		"spectra":  len(batch.Spectra),
		"message":  "Batch processing started with worker pool",
	}

	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(response)
}

// processBatchAsync handles asynchronous batch processing
func (h *BatchHandler) processBatchAsync(batch models.ImpedanceBatch) {
	batchStartTime := time.Now()
	eisMetricsDataResults := make([]models.EISMetricsDataResult, len(batch.Spectra))
	concurrency := h.getConcurrency()

	go func() {
		log.Printf("📦 Submitting %d jobs to worker pool", len(batch.Spectra))
		for _, spectrum := range batch.Spectra {
			job := h.createWorkItem(spectrum, *batch.BatchID)
			h.workerPool.SubmitJob(job)
		}
		log.Printf("✅ All jobs submitted to worker pool")
	}()

	resultsReceived := h.collectAllResults(len(batch.Spectra), eisMetricsDataResults)

	if resultsReceived < len(batch.Spectra) {
		log.Printf("⚠️ Missing results! Expected %d, received %d", len(batch.Spectra), resultsReceived)
	} else {
		log.Printf("✅ All %d results collected successfully", resultsReceived)
	}

	totalBatchTime := time.Since(batchStartTime)

	log.Printf("🔄 About to save timing results - BatchID: %s, Duration: %v, Concurrency: %d", *batch.BatchID, totalBatchTime, concurrency)

	h.saveTimingResults(*batch.BatchID, totalBatchTime, eisMetricsDataResults, concurrency)

	log.Printf("✅ CSV saving completed for batch: %s", *batch.BatchID)
}

// createWorkItem converts a batch item to a work item
func (h *BatchHandler) createWorkItem(item models.BatchItem, batchID string) models.WorkItem {
	// Convert to internal format with optimized data transformation
	freqs := item.ImpedanceData.Frequencies
	impData := make([][2]float64, len(item.ImpedanceData.Impedance))

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
		StartTime: time.Now(),
	}
}

// processResultWithImmediateWebhook processes a work result and sends webhook immediately
func (h *BatchHandler) processResultWithImmediateWebhook(result models.WorkResult, eisMetricsDataResults []models.EISMetricsDataResult) {
	index := result.Iteration
	if index < 0 || index >= len(eisMetricsDataResults) {
		log.Printf("ERROR: Invalid iteration index %d (iteration=%d, array_length=%d)", index, result.Iteration, len(eisMetricsDataResults))
		return
	}
	eisMetricsDataResults[index] = models.EISMetricsDataResult{
		Iteration:          result.Iteration,
		ProcessingTime:     result.ProcessingTime,
		ChiSquare:          result.Result.Min,
		Success:            result.Success,
		CircuitCode:        result.CircuitCode,
		OptimizationMethod: result.OptimizationMethod,
		Parameters:         result.Parameters,
	}

	webhook := models.WebhookItem{
		RequestID:         fmt.Sprintf("%s_iter_%03d", result.RequestID, result.Iteration),
		ChiSquare:         result.Result.Min,
		RealImp:           result.RealImp,
		ImagImp:           result.ImagImp,
		Freqs:             result.Freqs,
		Params:            result.Result.Params,
		Elements:          h.getElementNames(result.CircuitCode),
		ElementImpedances: h.calculateElementImpedances(result),
		CircuitCode:       result.CircuitCode,
	}

	h.workerPool.QueueWebhook(webhook)
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
		return []string{"r", "qy", "qn", "r"}
	}
}

// collectAllResults collects all results concurrently without chunking
func (h *BatchHandler) collectAllResults(expectedResults int, eisMetricsDataResults []models.EISMetricsDataResult) int {
	resultsReceived := 0
	perResultTimeout := 30 * time.Second

	for resultsReceived < expectedResults {
		result, ok := h.workerPool.GetResultBlocking(perResultTimeout)
		if !ok {
			log.Printf("⚠️ Timeout waiting for result %d/%d", resultsReceived+1, expectedResults)
			break
		}

		h.processResultWithImmediateWebhook(result, eisMetricsDataResults)
		resultsReceived++

		if resultsReceived%100 == 0 || resultsReceived == expectedResults {
			log.Printf("🚀 Progress: %d/%d results collected", resultsReceived, expectedResults)
		}
	}

	return resultsReceived
}

// calculateElementImpedances calculates impedance for each circuit element (simplified version)
func (h *BatchHandler) calculateElementImpedances(result models.WorkResult) []models.ElementImpedance {
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

	if wd, err := os.Getwd(); err == nil {
		filename = filepath.Join(wd, "concurrent_timing_results.csv")
		log.Printf("📁 Working directory: %s", wd)
		log.Printf("📄 Full CSV path: %s", filename)
	} else {
		log.Printf("⚠️  Could not get working directory: %v", err)
	}

	var writeHeader bool
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		writeHeader = true
	}

	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Error opening timing file: %v", err)
		return
	}
	defer file.Close()
	log.Printf("CSV file opened successfully")

	writer := csv.NewWriter(file)
	defer writer.Flush()

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

	theoreticalTime := avgSpectrumTime * time.Duration(numSpectra)
	efficiencyScore := theoreticalTime.Seconds() / totalTime.Seconds() / float64(concurrency)

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

	log.Printf("Writing timing record to CSV...")
	if err := writer.Write(record); err != nil {
		log.Printf("Error writing timing record: %v", err)
		return
	}
	log.Printf("Timing record written successfully")

	log.Printf("Timing saved: %d spectra, %d goroutines, %.2f ms total, %.2f%% success, %.3f efficiency",
		numSpectra, concurrency, float64(totalTime.Nanoseconds())/1000000.0, successRate, efficiencyScore)

	log.Printf("Now saving detailed spectrum results...")
	h.saveDetailedResults(batchID, eisMetricsDataResults)
	log.Printf("Detailed results saving completed")
}

// saveDetailedResults saves per-spectrum detailed results including circuit parameters
func (h *BatchHandler) saveDetailedResults(batchID string, eisMetricsDataResults []models.EISMetricsDataResult) {
	log.Printf("🗂️  Starting to save detailed results to CSV...")

	filename := "detailed_spectrum_results.csv"

	if wd, err := os.Getwd(); err == nil {
		filename = filepath.Join(wd, "detailed_spectrum_results.csv")
		log.Printf("📄 Detailed CSV path: %s", filename)
	} else {
		log.Printf("⚠️  Could not get working directory for detailed results: %v", err)
	}

	var writeHeader bool
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		writeHeader = true
	}

	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Error opening detailed results file: %v", err)
		return
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

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

	log.Printf("Detailed results saved: %d spectrum records to %s", len(eisMetricsDataResults), filename)
	log.Printf("All CSV writing operations completed successfully")
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
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
