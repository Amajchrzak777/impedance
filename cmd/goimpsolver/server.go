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

var globalConfig *Config

// SpectrumTiming tracks performance metrics for individual spectrum processing
type SpectrumTiming struct {
	Iteration     int           `json:"iteration"`
	ProcessingTime time.Duration `json:"processing_time_ms"`
	ChiSquare     float64       `json:"chi_square"`
	Success       bool          `json:"success"`
	CircuitCode   string        `json:"circuit_code"`
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
	http.HandleFunc("/eis-data", handleEISData)
	http.HandleFunc("/eis-data/batch", handleBatchEISData)

	log.Println("Starting HTTP server on port 8080...")
	log.Println("Endpoints available:")
	log.Println("  - Single: http://localhost:8080/eis-data")
	log.Println("  - Batch:  http://localhost:8080/eis-data/batch")

	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatal("Failed to start server:", err)
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

	log.Printf("Batch processing started - ID: %s, Spectra: %d", batch.BatchID, len(batch.Spectra))

	// Start timing for performance measurement
	batchStartTime := time.Now()

	// Process each spectrum in parallel goroutines with rate limiting
	var wg sync.WaitGroup
	
	// Get concurrency level from config, default to 5
	concurrency := 5
	if globalConfig != nil && globalConfig.Threads > 0 {
		concurrency = int(globalConfig.Threads)
	}
	
	semaphore := make(chan struct{}, concurrency) // Configurable concurrent processes
	
	// Track individual spectrum processing times
	
	spectrumTimings := make([]SpectrumTiming, len(batch.Spectra))
	timingMutex := sync.Mutex{}

	for _, item := range batch.Spectra {
		wg.Add(1)
		go func(batchItem BatchItem) {
			defer wg.Done()
			semaphore <- struct{}{}        // Acquire semaphore
			defer func() { <-semaphore }() // Release semaphore

			// Start timing for this individual spectrum
			spectrumStartTime := time.Now()

			// Generate unique ID for this spectrum
			requestID := generateID()

			// Convert to internal format
			freqs := batchItem.ImpedanceData.Frequencies
			impData := make([][2]float64, len(batchItem.ImpedanceData.Impedance))

			log.Printf("DEBUG: Processing spectrum %d with %d frequencies and %d impedance points",
				batchItem.Iteration, len(freqs), len(batchItem.ImpedanceData.Impedance))

			for i, point := range batchItem.ImpedanceData.Impedance {
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

			// Process EIS data
			result := processEISData(freqs, impData, globalConfig)

			// Record timing and results
			spectrumDuration := time.Since(spectrumStartTime)
			timingMutex.Lock()
			spectrumTimings[batchItem.Iteration] = SpectrumTiming{
				Iteration:      batchItem.Iteration,
				ProcessingTime: spectrumDuration,
				ChiSquare:      result.Min,
				Success:        result.Status == goimpcore.OK,
				CircuitCode:    globalConfig.Code,
			}
			timingMutex.Unlock()

			// Extract real and imaginary parts for webhook
			realImp := make([]float64, len(batchItem.ImpedanceData.Impedance))
			imagImp := make([]float64, len(batchItem.ImpedanceData.Impedance))
			for i, imp := range batchItem.ImpedanceData.Impedance {
				realImp[i] = imp["real"]
				imagImp[i] = imp["imag"]
			}

			// Send webhook with iteration number included in ID for proper ordering
			orderedRequestID := fmt.Sprintf("%s_iter_%03d", requestID, batchItem.Iteration)
			elements := goimpcore.GetElements(strings.ToLower(globalConfig.Code))
			elementImpedances := calculateElementImpedances(freqs, result.Params, elements)
			sendWebhook(orderedRequestID, result.Min, realImp, imagImp, freqs, result.Params, elements, elementImpedances, globalConfig.Code)

			if !globalConfig.Quiet {
				log.Printf("Processed spectrum iteration %d (ID: %s) - Chi-square: %.6e",
					batchItem.Iteration, orderedRequestID, result.Min)
			}
		}(item)
	}

	// Wait for all processing to complete
	go func() {
		wg.Wait()
		totalBatchTime := time.Since(batchStartTime)
		
		// Save timing results to file
		saveConcurrentTimingResults(batch.BatchID, totalBatchTime, spectrumTimings, concurrency)
		
		log.Printf("Batch processing completed - ID: %s, Total time: %v", batch.BatchID, totalBatchTime)
	}()

	// Return immediate response
	response := map[string]interface{}{
		"success":  true,
		"batch_id": batch.BatchID,
		"spectra":  len(batch.Spectra),
		"message":  "Batch processing started",
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
