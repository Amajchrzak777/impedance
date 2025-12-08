package handlers

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/kacperjurak/goimpcore"
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

	// Process data synchronously to return results immediately
	result := h.processSync(requestID, impedanceData)

	if !h.config.Quiet {
		log.Printf("HTTP Request received - ID: %s, Data points: %d", requestID, len(impedanceData.Frequencies))
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(result)
}

// processSync handles synchronous processing of EIS data and returns results
func (h *EISHandler) processSync(requestID string, impedanceData models.ImpedanceData) map[string]interface{} {
	// Convert ImpedanceData to internal format
	freqs := impedanceData.Frequencies
	impData := make([][2]float64, len(impedanceData.Impedance))

	for i, point := range impedanceData.Impedance {
		impData[i] = [2]float64{point["real"], point["imag"]}
	}

	// Process EIS data
	result := h.processor(freqs, impData, h.config)

	// Create response with fitted parameters
	response := map[string]interface{}{
		"success":      true,
		"request_id":   requestID,
		"message":      "Processing completed",
		"circuit_code": h.config.Code,
		"data_points":  len(freqs),
	}

	// Add results based on the actual result type
	if eisResult, ok := result.(goimpcore.Result); ok {
		response["chi_square"] = eisResult.Min
		response["status"] = eisResult.Status
		response["parameters"] = eisResult.Params
		response["optimization_method"] = h.config.OptimMethod
		response["smart_mode"] = h.config.SmartMode

		// For R(QR) circuit, add parameter names for clarity
		if h.config.Code == "R(QR)" && len(eisResult.Params) >= 4 {
			response["fitted_parameters"] = map[string]interface{}{
				"R1":   eisResult.Params[0],
				"Q_Y0": eisResult.Params[1],
				"Q_n":  eisResult.Params[2],
				"R2":   eisResult.Params[3],
			}
		}
	} else {
		response["error"] = "Invalid result type"
	}

	return response
}

// processAsync handles asynchronous processing of EIS data
func (h *EISHandler) processAsync(requestID string, impedanceData models.ImpedanceData) {
	freqs := impedanceData.Frequencies
	impData := make([][2]float64, len(impedanceData.Impedance))

	for i, point := range impedanceData.Impedance {
		impData[i] = [2]float64{point["real"], point["imag"]}
	}

	_ = h.processor(freqs, impData, h.config)

	realImp := make([]float64, len(impedanceData.Impedance))
	imagImp := make([]float64, len(impedanceData.Impedance))
	for i, imp := range impedanceData.Impedance {
		realImp[i] = imp["real"]
		imagImp[i] = imp["imag"]
	}

	webhook := models.WebhookItem{
		RequestID:   requestID,
		ChiSquare:   0.0,
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
