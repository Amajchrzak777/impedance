package models

import (
	"time"

	"github.com/kacperjurak/goimpcore"
)

// ImpedanceData represents incoming impedance measurement data
type ImpedanceData struct {
	Timestamp   string               `json:"timestamp"`
	Frequencies []float64            `json:"frequencies"`
	Magnitude   []float64            `json:"magnitude"`
	Phase       []float64            `json:"phase"`
	Impedance   []map[string]float64 `json:"impedance"`
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

// WorkItem represents a single EIS processing task
type WorkItem struct {
	ID        int
	RequestID string
	BatchID   string
	Iteration int
	Freqs     []float64
	ImpData   [][2]float64
	Config    interface{} // Will be properly typed when config package is created
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

// ElementImpedance represents impedance data for a circuit element
type ElementImpedance struct {
	Name       string               `json:"name"`
	Impedances []map[string]float64 `json:"impedances"`
}

// WebhookResponse represents the webhook payload structure
type WebhookResponse struct {
	ID                 string             `json:"id"`
	Time               string             `json:"time"`
	ChiSquare          float64            `json:"chi_square"`
	RealImpedance      []float64          `json:"real_impedance"`
	ImaginaryImpedance []float64          `json:"imaginary_impedance"`
	Frequencies        []float64          `json:"frequencies"`
	Parameters         []float64          `json:"parameters"`
	ElementNames       []string           `json:"element_names"`
	ElementImpedances  []ElementImpedance `json:"element_impedances"`
	CircuitType        string             `json:"circuit_type"`
}

// SpectrumTiming tracks performance metrics for individual spectrum processing
type SpectrumTiming struct {
	Iteration      int           `json:"iteration"`
	ProcessingTime time.Duration `json:"processing_time_ms"`
	ChiSquare      float64       `json:"chi_square"`
	Success        bool          `json:"success"`
	CircuitCode    string        `json:"circuit_code"`
}

// BufferSet contains reusable buffers to reduce allocations
type BufferSet struct {
	Real []float64
	Imag []float64
	Imp  [][2]float64
}
