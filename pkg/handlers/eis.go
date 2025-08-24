package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/kacperjurak/goimpcore/internal/utils"
	"github.com/kacperjurak/goimpcore/pkg/config"
	"github.com/kacperjurak/goimpcore/pkg/models"
	"github.com/kacperjurak/goimpcore/pkg/worker"
)

// EISHandler handles single EIS data processing requests
type EISHandler struct {
	config     *config.Config
	workerPool *worker.Pool
	processor  ProcessorFunc
}

// ProcessorFunc defines the signature for EIS data processing
type ProcessorFunc func(freqs []float64, impData [][2]float64, config *config.Config) interface{}

// NewEISHandler creates a new EIS handler
func NewEISHandler(cfg *config.Config, pool *worker.Pool, processor ProcessorFunc) *EISHandler {
	return &EISHandler{
		config:     cfg,
		workerPool: pool,
		processor:  processor,
	}
}

// ServeHTTP implements the http.Handler interface
func (h *EISHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.setupCORS(w)

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "POST" {
		h.writeError(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var impedanceData models.ImpedanceData
	if err := json.NewDecoder(r.Body).Decode(&impedanceData); err != nil {
		h.writeError(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	if len(impedanceData.Frequencies) == 0 {
		h.writeError(w, "No data points provided", http.StatusBadRequest)
		return
	}

	// Generate unique ID for this request
	requestID := utils.GenerateID()

	// Process data asynchronously
	go h.processAsync(requestID, impedanceData)

	// Return immediate response
	response := map[string]interface{}{
		"success":    true,
		"request_id": requestID,
		"message":    "Processing started",
	}

	if !h.config.Quiet {
		log.Printf("HTTP Request received - ID: %s, Data points: %d", requestID, len(impedanceData.Frequencies))
	}

	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(response)
}

// processAsync handles asynchronous processing of EIS data
func (h *EISHandler) processAsync(requestID string, impedanceData models.ImpedanceData) {
	// Convert ImpedanceData to internal format
	freqs := impedanceData.Frequencies
	impData := make([][2]float64, len(impedanceData.Impedance))

	for i, point := range impedanceData.Impedance {
		impData[i] = [2]float64{point["real"], point["imag"]}
	}

	// Process EIS data
	_ = h.processor(freqs, impData, h.config)

	// Extract real and imaginary parts for webhook
	realImp := make([]float64, len(impedanceData.Impedance))
	imagImp := make([]float64, len(impedanceData.Impedance))
	for i, imp := range impedanceData.Impedance {
		realImp[i] = imp["real"]
		imagImp[i] = imp["imag"]
	}

	// Create webhook item
	// TODO: Integrate with proper EIS result processing
	webhook := models.WebhookItem{
		RequestID:   requestID,
		ChiSquare:   0.0, // Will be extracted from result
		RealImp:     realImp,
		ImagImp:     imagImp,
		Freqs:       freqs,
		CircuitCode: h.config.Code,
	}

	h.workerPool.QueueWebhook(webhook)
}

// setupCORS sets up CORS headers
func (h *EISHandler) setupCORS(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

// writeError writes an error response
func (h *EISHandler) writeError(w http.ResponseWriter, message string, statusCode int) {
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
