package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kacperjurak/goimpcore"
)

var (
	globalConfig     *Config
	globalWorkerPool *WorkerPool
)

// SpectrumTiming tracks performance metrics for individual spectrum processing
type SpectrumTiming struct {
	Iteration      int           `json:"iteration"`
	ProcessingTime time.Duration `json:"processing_time_ms"`
	ChiSquare      float64       `json:"chi_square"`
	Success        bool          `json:"success"`
	CircuitCode    string        `json:"circuit_code"`
}

// WorkerPool manages a pool of workers for concurrent EIS processing
type WorkerPool struct {
	jobs         chan WorkItem
	results      chan WorkResult
	webhookQueue chan WebhookItem
	workers      int
	bufferPool   sync.Pool
	shutdown     chan struct{}
	wg           sync.WaitGroup
}

// WorkItem represents a single EIS processing task
type WorkItem struct {
	ID        int
	RequestID string
	BatchID   string
	Iteration int
	Freqs     []float64
	ImpData   [][2]float64
	Config    *Config
	StartTime time.Time
}

// WorkResult contains the result of EIS processing
type WorkResult struct {
	ID             int
	RequestID      string
	BatchID        string
	Iteration      int
	Result         goimpcore.Result
	ProcessingTime time.Duration
	Success        bool
	Freqs          []float64
	RealImp        []float64
	ImagImp        []float64
	CircuitCode    string
}

// WebhookItem represents a webhook task
type WebhookItem struct {
	RequestID         string
	ChiSquare         float64
	RealImp           []float64
	ImagImp           []float64
	Freqs             []float64
	Params            []float64
	Elements          []string
	ElementImpedances []ElementImpedance
	CircuitCode       string
}

// NewWorkerPool creates a new worker pool with specified number of workers
func NewWorkerPool(numWorkers int) *WorkerPool {
	wp := &WorkerPool{
		jobs:         make(chan WorkItem, numWorkers*2),
		results:      make(chan WorkResult, numWorkers*2),
		webhookQueue: make(chan WebhookItem, numWorkers*4),
		workers:      numWorkers,
		shutdown:     make(chan struct{}),
		bufferPool: sync.Pool{
			New: func() interface{} {
				return &BufferSet{
					Real: make([]float64, 0, 50),
					Imag: make([]float64, 0, 50),
					Imp:  make([][2]float64, 0, 50),
				}
			},
		},
	}

	// Start workers
	for i := 0; i < numWorkers; i++ {
		wp.wg.Add(1)
		go wp.worker(i)
	}

	// Start webhook processor
	wp.wg.Add(1)
	go wp.webhookProcessor()

	log.Printf("🔧 Worker pool started with %d workers", numWorkers)
	return wp
}

// BufferSet contains reusable buffers to reduce allocations
type BufferSet struct {
	Real []float64
	Imag []float64
	Imp  [][2]float64
}

// worker processes EIS jobs from the jobs channel
func (wp *WorkerPool) worker(id int) {
	defer wp.wg.Done()

	for {
		select {
		case job := <-wp.jobs:
			// Get buffer from pool
			buffers := wp.bufferPool.Get().(*BufferSet)

			// Reset buffers
			buffers.Real = buffers.Real[:0]
			buffers.Imag = buffers.Imag[:0]

			// Process EIS data
			startTime := time.Now()
			result := processEISData(job.Freqs, job.ImpData, job.Config)
			processingTime := time.Since(startTime)

			// Extract impedance data with pre-allocated buffers
			if cap(buffers.Real) < len(job.ImpData) {
				buffers.Real = make([]float64, len(job.ImpData))
				buffers.Imag = make([]float64, len(job.ImpData))
			} else {
				buffers.Real = buffers.Real[:len(job.ImpData)]
				buffers.Imag = buffers.Imag[:len(job.ImpData)]
			}

			for i, imp := range job.ImpData {
				buffers.Real[i] = imp[0]
				buffers.Imag[i] = imp[1]
			}

			// Create copies for result (buffers will be reused)
			realCopy := make([]float64, len(buffers.Real))
			imagCopy := make([]float64, len(buffers.Imag))
			copy(realCopy, buffers.Real)
			copy(imagCopy, buffers.Imag)

			// Send result
			wp.results <- WorkResult{
				ID:             job.ID,
				RequestID:      job.RequestID,
				BatchID:        job.BatchID,
				Iteration:      job.Iteration,
				Result:         result,
				ProcessingTime: processingTime,
				Success:        result.Status == goimpcore.OK,
				Freqs:          job.Freqs,
				RealImp:        realCopy,
				ImagImp:        imagCopy,
				CircuitCode:    job.Config.Code,
			}

			// Return buffers to pool
			wp.bufferPool.Put(buffers)

		case <-wp.shutdown:
			return
		}
	}
}

// webhookProcessor handles webhook requests asynchronously
func (wp *WorkerPool) webhookProcessor() {
	defer wp.wg.Done()

	for {
		select {
		case webhook := <-wp.webhookQueue:
			// Process webhook asynchronously without blocking workers
			go sendWebhook(webhook.RequestID, webhook.ChiSquare, webhook.RealImp, webhook.ImagImp,
				webhook.Freqs, webhook.Params, webhook.Elements, webhook.ElementImpedances, webhook.CircuitCode)

		case <-wp.shutdown:
			return
		}
	}
}

// SubmitJob submits a job to the worker pool
func (wp *WorkerPool) SubmitJob(job WorkItem) {
	select {
	case wp.jobs <- job:
		// Job submitted successfully
	default:
		log.Printf("⚠️  Worker pool jobs channel full, job may be delayed")
		wp.jobs <- job // Block until space available
	}
}

// GetResult retrieves a result from the worker pool (non-blocking)
func (wp *WorkerPool) GetResult() (WorkResult, bool) {
	select {
	case result := <-wp.results:
		return result, true
	default:
		return WorkResult{}, false
	}
}

// QueueWebhook queues a webhook for async processing
func (wp *WorkerPool) QueueWebhook(webhook WebhookItem) {
	select {
	case wp.webhookQueue <- webhook:
		// Webhook queued successfully
	default:
		log.Printf("⚠️  Webhook queue full, dropping webhook for %s", webhook.RequestID)
	}
}

// Shutdown gracefully shuts down the worker pool
func (wp *WorkerPool) Shutdown() {
	log.Printf("🛑 Shutting down worker pool...")
	close(wp.shutdown)
	wp.wg.Wait()
	log.Printf("✅ Worker pool shutdown complete")
}

// BatchItem represents a single spectrum with iteration number
type BatchItem struct {
	ImpedanceData ImpedanceData `json:"impedance_data"`
	Iteration     int           `json:"iteration"`
}

// ImpedanceBatch represents a batch of impedance measurements
type ImpedanceBatch struct {
	BatchID   string      `json:"batch_id"`
	Timestamp time.Time   `json:"timestamp"`
	Spectra   []BatchItem `json:"spectra"`
}

func startHTTPServer(cfg *Config) {
	globalConfig = cfg

	// Initialize optimized worker pool
	workerCount := 5
	if cfg.Threads > 0 {
		workerCount = int(cfg.Threads)
	}
	globalWorkerPool = NewWorkerPool(workerCount)

	// Setup graceful shutdown
	go func() {
		// This could be enhanced with signal handling for production
		// For now, the worker pool will be cleaned up when the process exits
	}()

	http.HandleFunc("/eis-data", handleEISData)
	http.HandleFunc("/eis-data/batch", handleBatchEISData)

	log.Println("🚀 Starting HTTP server on port 8080...")
	log.Println("📡 Endpoints available:")
	log.Println("  - Single: http://localhost:8080/eis-data")
	log.Println("  - Batch:  http://localhost:8080/eis-data/batch")

	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal("❌ Failed to start server:", err)
	}
}

func handleEISData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "POST" {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var impedanceData ImpedanceData
	if err := json.NewDecoder(r.Body).Decode(&impedanceData); err != nil {
		http.Error(w, `{"error":"Invalid JSON format"}`, http.StatusBadRequest)
		return
	}

	if len(impedanceData.Frequencies) == 0 {
		http.Error(w, `{"error":"No data points provided"}`, http.StatusBadRequest)
		return
	}

	// Generate unique ID for this request
	requestID := generateID()

	// Convert ImpedanceData to internal format
	freqs := impedanceData.Frequencies
	impData := make([][2]float64, len(impedanceData.Impedance))

	for i, point := range impedanceData.Impedance {
		impData[i] = [2]float64{point["real"], point["imag"]}
	}

	// Process data asynchronously and send webhook
	go func() {
		result := processEISData(freqs, impData, globalConfig)

		// Extract real and imaginary parts for webhook
		realImp := make([]float64, len(impedanceData.Impedance))
		imagImp := make([]float64, len(impedanceData.Impedance))
		for i, imp := range impedanceData.Impedance {
			realImp[i] = imp["real"]
			imagImp[i] = imp["imag"]
		}

		// Use actual chi-square from EIS processing result
		elements := goimpcore.GetElements(strings.ToLower(globalConfig.Code))
		elementImpedances := calculateElementImpedances(freqs, result.Params, elements)
		sendWebhook(requestID, result.Min, realImp, imagImp, freqs, result.Params, elements, elementImpedances, globalConfig.Code)
	}()

	// Return immediate response with request ID
	response := map[string]interface{}{
		"success":    true,
		"request_id": requestID,
		"message":    "Processing started",
	}

	if !globalConfig.Quiet {
		log.Printf("HTTP Request received - ID: %s, Data points: %d", requestID, len(impedanceData.Frequencies))
	}

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(response)
}

func handleBatchEISData(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "POST" {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var batch ImpedanceBatch
	if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
		http.Error(w, `{"error":"Invalid JSON format"}`, http.StatusBadRequest)
		return
	}

	if len(batch.Spectra) == 0 {
		http.Error(w, `{"error":"No spectra provided in batch"}`, http.StatusBadRequest)
		return
	}

	log.Printf("🔄 Batch processing started - ID: %s, Spectra: %d", batch.BatchID, len(batch.Spectra))

	// Start timing for performance measurement
	batchStartTime := time.Now()

	// Prepare data structures for optimized processing
	spectrumTimings := make([]SpectrumTiming, len(batch.Spectra))
	resultsReceived := 0

	// Process batch using optimized worker pool
	go func() {
		// Submit all jobs to worker pool
		for _, item := range batch.Spectra {
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

			// Create work item for worker pool
			job := WorkItem{
				ID:        item.Iteration,
				RequestID: generateID(),
				BatchID:   batch.BatchID,
				Iteration: item.Iteration,
				Freqs:     freqs,
				ImpData:   impData,
				Config:    globalConfig,
				StartTime: time.Now(),
			}

			// Submit to worker pool
			globalWorkerPool.SubmitJob(job)
		}

		// Collect results from worker pool
		for resultsReceived < len(batch.Spectra) {
			if result, ok := globalWorkerPool.GetResult(); ok {
				// Record timing (lock-free via channels)
				spectrumTimings[result.Iteration] = SpectrumTiming{
					Iteration:      result.Iteration,
					ProcessingTime: result.ProcessingTime,
					ChiSquare:      result.Result.Min,
					Success:        result.Success,
					CircuitCode:    result.CircuitCode,
				}

				// Queue webhook for async processing
				elements := goimpcore.GetElements(strings.ToLower(globalConfig.Code))
				elementImpedances := calculateElementImpedances(result.Freqs, result.Result.Params, elements)

				webhook := WebhookItem{
					RequestID:         fmt.Sprintf("%s_iter_%03d", result.RequestID, result.Iteration),
					ChiSquare:         result.Result.Min,
					RealImp:           result.RealImp,
					ImagImp:           result.ImagImp,
					Freqs:             result.Freqs,
					Params:            result.Result.Params,
					Elements:          elements,
					ElementImpedances: elementImpedances,
					CircuitCode:       result.CircuitCode,
				}

				globalWorkerPool.QueueWebhook(webhook)

				if !globalConfig.Quiet {
					log.Printf("✅ Processed spectrum iteration %d - Chi-square: %.6e",
						result.Iteration, result.Result.Min)
				}

				resultsReceived++
			} else {
				// No results available yet, small delay to prevent busy waiting
				time.Sleep(1 * time.Millisecond)
			}
		}

		// All results collected
		totalBatchTime := time.Since(batchStartTime)

		// Get concurrency level for timing results
		concurrency := 5
		if globalConfig != nil && globalConfig.Threads > 0 {
			concurrency = int(globalConfig.Threads)
		}

		// Save timing results to file
		saveConcurrentTimingResults(batch.BatchID, totalBatchTime, spectrumTimings, concurrency)

		log.Printf("🎉 Batch processing completed - ID: %s, Total time: %v", batch.BatchID, totalBatchTime)
	}()

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

// saveConcurrentTimingResults saves timing data to a CSV file for performance analysis
func saveConcurrentTimingResults(batchID string, totalTime time.Duration, spectrumTimings []SpectrumTiming, concurrency int) {
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
