package webhook

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/kacperjurak/goimpcore/pkg/config"
	"github.com/kacperjurak/goimpcore/pkg/models"
)

// Client handles webhook HTTP requests with optimized connection pooling
type Client struct {
	url        string
	httpClient *http.Client
	config     *config.Config
	bufferPool sync.Pool // Pool for JSON marshaling buffers
}

// NewClient creates a new webhook client with optimized connection pooling
func NewClient(url string, cfg *config.Config) *Client {
	// Create optimized transport with connection pooling
	transport := &http.Transport{
		// Connection pooling settings
		MaxIdleConns:        100,              // Maximum idle connections
		MaxIdleConnsPerHost: 20,               // Maximum idle connections per host
		IdleConnTimeout:     90 * time.Second, // Idle connection timeout

		// Dial timeout settings
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second, // Connection timeout
			KeepAlive: 30 * time.Second, // Keep-alive probe interval
		}).DialContext,

		// TLS settings
		TLSHandshakeTimeout: 10 * time.Second,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: false, // Set to true for development only
		},

		// Response header timeout
		ResponseHeaderTimeout: 30 * time.Second,

		// Disable compression for better performance on small payloads
		DisableCompression: true,

		// Force HTTP/1.1 for better connection reuse
		ForceAttemptHTTP2: false,
	}

	client := &Client{
		url:    url,
		config: cfg,
		httpClient: &http.Client{
			Timeout:   45 * time.Second, // Total request timeout
			Transport: transport,
		},
		// Buffer pool for JSON marshaling to reduce GC pressure
		bufferPool: sync.Pool{
			New: func() interface{} {
				return bytes.NewBuffer(make([]byte, 0, 1024)) // Pre-allocate 1KB buffer
			},
		},
	}

	return client
}

// Send sends a webhook with the provided data
func (c *Client) Send(webhook models.WebhookItem) error {
	// Validate and clean data for JSON marshaling
	validChiSquare := c.sanitizeFloat(webhook.ChiSquare)
	if validChiSquare != webhook.ChiSquare {
		log.Printf("Warning: Chi-square sanitized from %v to %v", webhook.ChiSquare, validChiSquare)
	}

	// Create webhook response payload
	payload := models.WebhookResponse{
		ID:                 webhook.RequestID,
		Time:               time.Now().Format(time.RFC3339Nano),
		ChiSquare:          validChiSquare,
		RealImpedance:      webhook.RealImp,
		ImaginaryImpedance: webhook.ImagImp,
		Frequencies:        webhook.Freqs,
		Parameters:         webhook.Params,
		ElementNames:       webhook.Elements,
		ElementImpedances:  webhook.ElementImpedances,
		CircuitType:        webhook.CircuitCode,
	}

	// Get buffer from pool and marshal to JSON
	buf := c.bufferPool.Get().(*bytes.Buffer)
	buf.Reset()                 // Clear buffer
	defer c.bufferPool.Put(buf) // Return to pool

	encoder := json.NewEncoder(buf)
	if err := encoder.Encode(payload); err != nil {
		return fmt.Errorf("failed to marshal webhook data: %w", err)
	}

	// Log debug information if not in quiet mode
	if !c.config.Quiet {
		log.Printf("DEBUG: Webhook payload - CircuitType: %s, ElementNames: %v",
			payload.CircuitType, payload.ElementNames)
	}

	// Send HTTP request with pooled buffer
	resp, err := c.httpClient.Post(c.url, "application/json", bytes.NewReader(buf.Bytes()))
	if err != nil {
		return fmt.Errorf("failed to send webhook: %w", err)
	}
	defer resp.Body.Close()

	// Log success if not in quiet mode
	if !c.config.Quiet {
		log.Printf("Webhook sent - ID: %s, Chi-square: %.14e, CircuitType: %s, Status: %d",
			webhook.RequestID, webhook.ChiSquare, webhook.CircuitCode, resp.StatusCode)
	}

	// Check for HTTP errors
	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook request failed with status %d", resp.StatusCode)
	}

	return nil
}

// sanitizeFloat cleans float64 values for JSON compatibility
func (c *Client) sanitizeFloat(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0.0
	}
	return value
}
