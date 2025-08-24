package handlers

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
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

	log.Printf("🔄 Batch processing started - ID: %s, Spectra: %d", batch.BatchID, len(batch.Spectra))

	// Process batch asynchronously
	go h.processBatchAsync(batch)

	// Return immediate response
	response := map[string]interface{}{
		"success":  true,
		"batch_id": batch.BatchID,
		"spectra":  len(batch.Spectra),
		"message":  "Batch processing started with worker pool",
	}

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(response)
}

// processBatchAsync handles asynchronous batch processing
func (h *BatchHandler) processBatchAsync(batch models.ImpedanceBatch) {
	batchStartTime := time.Now()
	spectrumTimings := make([]models.SpectrumTiming, len(batch.Spectra))
	resultsReceived := 0

	// Submit all jobs to worker pool
	for _, item := range batch.Spectra {
		job := h.createWorkItem(item, batch.BatchID)
		h.workerPool.SubmitJob(job)
	}

	// Collect results from worker pool
	for resultsReceived < len(batch.Spectra) {
		if result, ok := h.workerPool.GetResult(); ok {
			h.processResult(result, spectrumTimings)
			resultsReceived++
		} else {
			// No results available yet, small delay to prevent busy waiting
			time.Sleep(1 * time.Millisecond)
		}
	}

	// All results collected
	totalBatchTime := time.Since(batchStartTime)
	concurrency := h.getConcurrency()

	// Save timing results to file
	h.saveTimingResults(batch.BatchID, totalBatchTime, spectrumTimings, concurrency)

	log.Printf("🎉 Batch processing completed - ID: %s, Total time: %v", batch.BatchID, totalBatchTime)
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
		StartTime: time.Now(),
	}
}

// processResult processes a work result and updates timing
func (h *BatchHandler) processResult(result models.WorkResult, spectrumTimings []models.SpectrumTiming) {
	// Record timing
	spectrumTimings[result.Iteration] = models.SpectrumTiming{
		Iteration:      result.Iteration,
		ProcessingTime: result.ProcessingTime,
		ChiSquare:      result.Result.Min, // Extract chi-square from EIS result
		Success:        result.Success,
		CircuitCode:    result.CircuitCode,
	}

	// Create webhook item
	// TODO: Integrate with proper element calculation
	webhook := models.WebhookItem{
		RequestID:   fmt.Sprintf("%s_iter_%03d", result.RequestID, result.Iteration),
		ChiSquare:   result.Result.Min, // Extract chi-square from EIS result
		RealImp:     result.RealImp,
		ImagImp:     result.ImagImp,
		Freqs:       result.Freqs,
		CircuitCode: result.CircuitCode,
	}

	h.workerPool.QueueWebhook(webhook)

	if !h.config.Quiet {
		log.Printf("✅ Processed spectrum iteration %d", result.Iteration)
	}
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
func (h *BatchHandler) saveTimingResults(batchID string, totalTime time.Duration, spectrumTimings []models.SpectrumTiming, concurrency int) {
	filename := "concurrent_timing_results.csv"

	// Check if file exists to decide on header
	var writeHeader bool
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		writeHeader = true
	}

	// Open file for append
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Printf("Error opening timing file: %v", err)
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

	for _, timing := range spectrumTimings {
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

	numSpectra := len(spectrumTimings)
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

	// Get circuit code from first spectrum timing (should be consistent across all spectra)
	circuitCode := "Unknown"
	if len(spectrumTimings) > 0 {
		circuitCode = spectrumTimings[0].CircuitCode
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
	}

	if err := writer.Write(record); err != nil {
		log.Printf("Error writing timing record: %v", err)
		return
	}

	log.Printf("📊 Timing saved: %d spectra, %d goroutines, %.2f ms total, %.2f%% success, %.3f efficiency",
		numSpectra, concurrency, float64(totalTime.Nanoseconds())/1000000.0, successRate, efficiencyScore)
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
