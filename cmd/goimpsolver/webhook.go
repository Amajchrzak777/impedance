package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"math"
	"net/http"
	"time"
)

const webhookURL = "http://webplot:3001/webhook"

type WebhookResponse struct {
	ID                 string    `json:"id"`
	Time               string    `json:"time"`
	ChiSquare          float64   `json:"chi_square"`
	RealImpedance      []float64 `json:"real_impedance"`
	ImaginaryImpedance []float64 `json:"imaginary_impedance"`
	Frequencies        []float64 `json:"frequencies"`
	Parameters         []float64 `json:"parameters"`
	ElementNames       []string  `json:"element_names"`
}

func generateID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}

func sendWebhook(requestID string, chiSquare float64, realImp []float64, imagImp []float64, frequencies []float64, parameters []float64, elementNames []string) {
	// Handle NaN, Inf and other invalid float64 values for JSON marshaling
	validChiSquare := chiSquare
	if math.IsNaN(chiSquare) || math.IsInf(chiSquare, 0) {
		validChiSquare = 0.0 // Set to 0 instead of NaN for JSON compatibility
		log.Printf("Warning: Chi-square is invalid (%v), setting to 0.0 for JSON", chiSquare)
	}

	webhookData := WebhookResponse{
		ID:                 requestID,
		Time:               time.Now().Format(time.RFC3339Nano),
		ChiSquare:          validChiSquare,
		RealImpedance:      realImp,
		ImaginaryImpedance: imagImp,
		Frequencies:        frequencies,
		Parameters:         parameters,
		ElementNames:       elementNames,
	}

	jsonData, err := json.Marshal(webhookData)
	if err != nil {
		log.Printf("Error marshaling webhook data: %v", err)
		return
	}

	resp, err := http.Post(webhookURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Error sending webhook: %v", err)
		return
	}
	defer resp.Body.Close()

	if !globalConfig.Quiet {
		log.Printf("Webhook sent - ID: %s, Chi-square: %.14e, Status: %d", requestID, chiSquare, resp.StatusCode)
	}
}
