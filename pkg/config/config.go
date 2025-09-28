package config

import (
	"net"
	"strconv"
	"time"
)

// ArrayFlags replacement for removed goimp/cmd.ArrayFlags
type ArrayFlags []float64

func (a *ArrayFlags) String() string {
	return "ArrayFlags"
}

func (a *ArrayFlags) Set(value string) (err error) {
	if val, err := strconv.ParseFloat(value, 64); err == nil {
		*a = append(*a, val)
	}

	return err
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
		OptimMethod: "levenberg-marquardt", // Default fallback, will be overridden by CLI
		SmartMode:   "eis",
		ImgDPI:      300,
		ImgSize:     800,
		Quiet:       false,
		HTTPServer:  true,
		Unity:       true,
	}
}

// DefaultServerConfig returns server configuration with sensible defaults
func DefaultServerConfig() *ServerConfig {
	webhookURL := getWebhookURL()
	return &ServerConfig{
		Port:            "8080",
		WorkerCount:     5,
		WebhookURL:      webhookURL,
		EnableMetrics:   true,
		EnableProfiling: false,
		ProfilingPort:   "6060",
	}
}

// getWebhookURL determines the correct webhook URL based on environment
func getWebhookURL() string {
	// Check if we're running in Docker by looking for the webplot hostname
	// In Docker, container names resolve as hostnames
	// Outside Docker, use localhost

	// Try to resolve webplot hostname - if it fails, we're running locally
	conn, err := net.DialTimeout("tcp", "webplot:3001", 1*time.Second)
	if err == nil {
		// webplot hostname is reachable, we're in Docker
		conn.Close()
		return "http://webplot:3001/webhook"
	}

	// webplot hostname not reachable, use localhost
	return "http://localhost:3001/webhook"
}
