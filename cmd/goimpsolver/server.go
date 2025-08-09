package main

import (
	"encoding/json"
	"log"
	"net/http"
)

var globalConfig *Config

func startHTTPServer(cfg *Config) {
	globalConfig = cfg
	http.HandleFunc("/eis-data", handleEISData)

	log.Println("Starting HTTP server on port 8080...")
	log.Println("Endpoint available at: http://localhost:8080/eis-data")

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
		sendWebhook(requestID, result.Min, realImp, imagImp)
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
