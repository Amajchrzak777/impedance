package config

import (
	"strconv"
)

// ArrayFlags replacement for removed goimp/cmd.ArrayFlags
type ArrayFlags []float64

func (a *ArrayFlags) String() string {
	return "ArrayFlags"
}

func (a *ArrayFlags) Set(value string) error {
	if val, err := strconv.ParseFloat(value, 64); err == nil {
		*a = append(*a, val)
		return nil
	} else {
		return err
	}
}

// Config holds all configuration settings for the EIS solver
type Config struct {
	Code            string
	File            string
	InitValues      ArrayFlags
	CutLow          uint
	CutHigh         uint
	Unity           bool
	SmartMode       string
	OptimMethod     string
	Benchmark       bool
	Flip            bool
	ImgOut          bool
	ImgSave         bool
	ImgPath         string
	ImgDPI          uint
	ImgSize         uint
	Concurrency     bool
	Threads         uint
	Jobs            uint
	Quiet           bool
	HTTPServer      bool
	EnableProfiling bool
}

// ServerConfig holds server-specific configuration
type ServerConfig struct {
	Port            string
	WorkerCount     int
	WebhookURL      string
	EnableMetrics   bool
	EnableProfiling bool
	ProfilingPort   string
}

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		Code:        "R(QR)",
		Threads:     5,
		OptimMethod: "nelder-mead",
		SmartMode:   "eis",
		ImgDPI:      300,
		ImgSize:     800,
		Quiet:       false,
		HTTPServer:  true,
	}
}

// DefaultServerConfig returns server configuration with sensible defaults
func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		Port:            "8080",
		WorkerCount:     5,
		WebhookURL:      "http://webplot:3001/webhook",
		EnableMetrics:   true,
		EnableProfiling: false,
		ProfilingPort:   "6060",
	}
}
