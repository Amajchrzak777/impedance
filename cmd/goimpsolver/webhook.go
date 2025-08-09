package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
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
}

func generateID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}

func sendWebhook(requestID string, chiSquare float64, realImp []float64, imagImp []float64) {
	webhookData := WebhookResponse{
		ID:                 requestID,
		Time:               time.Now().Format(time.RFC3339Nano),
		ChiSquare:          chiSquare,
		RealImpedance:      realImp,
		ImaginaryImpedance: imagImp,
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
