package main

import (
	"strconv"
)

// ArrayFlags replacement for removed goimp/cmd.ArrayFlags
type ArrayFlags []float64

func (a *ArrayFlags) String() string {
	return "ArrayFlags"
}

func (a *ArrayFlags) Set(value string) error {
	// Parse the string value as float64 and append to the array
	if val, err := strconv.ParseFloat(value, 64); err == nil {
		*a = append(*a, val)
		return nil
	} else {
		return err
	}
}

type Config struct {
	Code         string
	File         string
	InitValues   ArrayFlags  // Changed from cmd.ArrayFlags
	CutLow       uint
	CutHigh      uint
	Unity        bool
	SmartMode    string
	OptimMethod  string      // New field for optimization method selection
	Benchmark    bool        // Enable benchmark mode with timing
	Flip         bool
	ImgOut       bool
	ImgSave      bool
	ImgPath      string
	ImgDPI       uint
	ImgSize      uint
	Concurrency  bool
	Threads      uint
	Jobs         uint
	Quiet        bool
	HTTPServer   bool
}

type EISDataPoint struct {
	Frequency float64 `json:"frequency"`
	Real      float64 `json:"real"`
	Imag      float64 `json:"imag"`
}

// ImpedanceData matches the format sent by mockinput
type ImpedanceData struct {
	Timestamp   string                   `json:"timestamp"`
	Frequencies []float64                `json:"frequencies"`
	Magnitude   []float64                `json:"magnitude"`
	Phase       []float64                `json:"phase"`
	Impedance   []map[string]float64     `json:"impedance"`
}
