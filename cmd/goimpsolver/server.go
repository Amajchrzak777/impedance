package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"
	
	"github.com/kacperjurak/goimpcore"
)

var globalConfig *Config

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
		sendWebhook(requestID, result.Min, realImp, imagImp, freqs, result.Params, elements)
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

	// Process each spectrum in parallel goroutines with rate limiting
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 5) // Limit to 5 concurrent processes

	for _, item := range batch.Spectra {
		wg.Add(1)
		go func(batchItem BatchItem) {
			defer wg.Done()
			semaphore <- struct{}{}        // Acquire semaphore
			defer func() { <-semaphore }() // Release semaphore

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
			sendWebhook(orderedRequestID, result.Min, realImp, imagImp, freqs, result.Params, elements)

			if !globalConfig.Quiet {
				log.Printf("Processed spectrum iteration %d (ID: %s) - Chi-square: %.6e",
					batchItem.Iteration, orderedRequestID, result.Min)
			}
		}(item)
	}

	// Wait for all processing to complete
	go func() {
		wg.Wait()
		log.Printf("Batch processing completed - ID: %s", batch.BatchID)
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
