package server

import (
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/kacperjurak/goimpcore"
	"github.com/kacperjurak/goimpcore/pkg/config"
	"github.com/kacperjurak/goimpcore/pkg/handlers"
	"github.com/kacperjurak/goimpcore/pkg/profiling"
	"github.com/kacperjurak/goimpcore/pkg/webhook"
	"github.com/kacperjurak/goimpcore/pkg/worker"
)

const (
	minFunc       = 1.35e-2
	maxIterations = 10
)

// Server represents the HTTP server with all dependencies
type Server struct {
	config        *config.Config
	serverConfig  *config.ServerConfig
	workerPool    *worker.Pool
	webhookClient *webhook.Client
	httpServer    *http.Server
	profiler      *profiling.Profiler
	middleware    *profiling.Middleware
}

// ProcessorFunc defines the signature for EIS data processing
type ProcessorFunc func(freqs []float64, impData [][2]float64, config *config.Config) interface{}

// Options holds configuration for creating a new server
type Options struct {
	Config       *config.Config
	ServerConfig *config.ServerConfig
	Processor    ProcessorFunc
}

// New creates a new server instance
func New(opts Options) *Server {
	if opts.Config == nil {
		opts.Config = config.DefaultConfig()
	}
	if opts.ServerConfig == nil {
		opts.ServerConfig = config.DefaultServerConfig()
	}

	// Create worker pool
	workerPool := worker.New(worker.Options{
		Workers:   opts.ServerConfig.WorkerCount,
		Processor: worker.ProcessorFunc(opts.Processor),
	})

	// Create webhook client
	webhookClient := webhook.NewClient(opts.ServerConfig.WebhookURL, opts.Config)

	// Create profiler and middleware
	profiler := profiling.New(opts.ServerConfig)
	middleware := profiling.NewMiddleware(opts.ServerConfig.EnableProfiling)

	// Create HTTP server
	server := &Server{
		config:        opts.Config,
		serverConfig:  opts.ServerConfig,
		workerPool:    workerPool,
		webhookClient: webhookClient,
		profiler:      profiler,
		middleware:    middleware,
	}

	server.setupRoutes()
	return server
}

// setupRoutes configures HTTP routes and handlers
func (s *Server) setupRoutes() {
	mux := http.NewServeMux()

	// Create handlers
	eisHandler := handlers.NewEISHandler(s.config, s.workerPool, s.getProcessorFunc())
	batchHandler := handlers.NewBatchHandler(s.config, s.workerPool, s.getProcessorFunc())

	// Register routes with profiling middleware
	mux.Handle("/eis-data", s.middleware.ProfiledHandler("eis-single", eisHandler))
	mux.Handle("/eis-data/batch", s.middleware.ProfiledHandler("eis-batch", batchHandler))
	mux.HandleFunc("/health", s.healthHandler)
	mux.HandleFunc("/debug/gc", s.gcHandler)
	mux.HandleFunc("/debug/memory", s.memoryHandler)

	s.httpServer = &http.Server{
		Addr:         ":" + s.serverConfig.Port,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
}

// getProcessorFunc returns the actual EIS processor function
func (s *Server) getProcessorFunc() handlers.ProcessorFunc {
	return func(freqs []float64, impData [][2]float64, cfg *config.Config) interface{} {
		return s.processEISData(freqs, impData, cfg)
	}
}

// processEISData performs actual EIS processing using goimpcore
func (s *Server) processEISData(freqs []float64, impData [][2]float64, cfg *config.Config) goimpcore.Result {
	log.Printf("🔥 DEBUG: processEISData called with %d frequencies, config: %+v", len(freqs), cfg)
	log.Printf("🔥 DEBUG: Starting actual EIS processing...")

	code := strings.ToLower(cfg.Code)

	if cfg.OptimMethod == "all" {
		return s.runAllOptimizationMethods(code, freqs, impData, cfg)
	}

	return s.runSingleOptimizationMethod(code, freqs, impData, cfg, cfg.OptimMethod)
}

func (s *Server) runSingleOptimizationMethod(code string, freqs []float64, impData [][2]float64, cfg *config.Config, method string) goimpcore.Result {
	solver := goimpcore.NewSolver(code, freqs, impData)

	// Use provided InitValues or generate automatic ones
	if len(cfg.InitValues) > 0 {
		solver.InitValues = []float64(cfg.InitValues)
		log.Printf("Using provided initial values: %v", solver.InitValues)
	} else {
		solver.InitValues = s.generateInitialValues(code)
		log.Printf("Using auto-generated initial values: %v", solver.InitValues)
	}

	if cfg.Unity {
		solver.Weighting = goimpcore.UNITY
	} else {
		solver.Weighting = goimpcore.MODULUS
	}

	// Set the solver method based on the optimization method
	switch method {
	case "nelder-mead":
		solver.SmartMode = "eis" // Use EIS smart mode for multi-try approach
	case "levenberg-marquardt", "lm":
		solver.SmartMode = "lm"
	case "gradient-descent", "gd":
		solver.SmartMode = "gd"
	case "lbfgs":
		solver.SmartMode = "lbfgs"
	case "newton":
		solver.SmartMode = "newton"
	default:
		log.Printf("Unknown optimization method '%s', using Nelder-Mead", method)
		solver.SmartMode = "eis"
	}

	log.Printf("Using optimization method: %s", method)

	// Time the optimization
	startTime := time.Now()
	res := solver.Solve(minFunc, maxIterations)
	duration := time.Since(startTime)

	// Ensure consistent chi-square calculation for all methods
	// Skip recalculation for EIS mode as it handles scaling internally
	if res.Status != "ERROR" && len(res.Params) > 0 && (res.MinUnit != "ChiSq" || method != "levenberg-marquardt") && cfg.SmartMode != "eis" {
		// Debug the recalculation process
		theoreticalImp := goimpcore.CircuitImpedance(code, freqs, res.Params)

		actualChiSq := goimpcore.ChiSq(impData, theoreticalImp, solver.Weighting)
		log.Printf("DEBUG: ChiSq calculation result: %v (weighting: %v)", actualChiSq, solver.Weighting)

		// Check if recalculation produces NaN
		if math.IsNaN(actualChiSq) || math.IsInf(actualChiSq, 0) {
			log.Printf("WARNING: Recalculated chi-square is invalid (%v), keeping original result.Min (%v)", actualChiSq, res.Min)
		} else {
			log.Printf("INFO: Using recalculated chi-square (%v) instead of original (%v)", actualChiSq, res.Min)
			res.Min = actualChiSq
			res.MinUnit = "ChiSq"
		}
	} else if cfg.SmartMode == "eis" {
		log.Printf("INFO: Skipping chi-square recalculation for EIS mode (scaling handled internally)")
	}

	if res.Status == "ERROR" {
		log.Printf("EIS processing FAILED - Method: %s, Status: %s", method, res.Status)
	} else {
		log.Printf("EIS processing completed - Method: %s, Chi-square: %.14e", method, res.Min)
	}

	if !cfg.Quiet {
		if res.Status == "ERROR" {
			log.Printf("Method: %s FAILED - Status=%s", method, res.Status)
		} else {
			log.Printf("Method: %s, Min=%.12e, Params=%v, Status=%s", method, res.Min, res.Params, res.Status)
		}
	}

	log.Printf("Processing time: %v", duration)
	return res
}

func (s *Server) runAllOptimizationMethods(code string, freqs []float64, impData [][2]float64, cfg *config.Config) goimpcore.Result {
	methods := []string{"nelder-mead", "levenberg-marquardt", "gradient-descent", "lbfgs", "newton"}
	var bestResult goimpcore.Result
	bestChiSq := math.Inf(1)

	log.Printf("Running all optimization methods for comparison...")

	for _, method := range methods {
		log.Printf("Testing method: %s", method)
		result := s.runSingleOptimizationMethod(code, freqs, impData, cfg, method)

		if result.Status != "ERROR" && result.Min < bestChiSq {
			bestResult = result
			bestChiSq = result.Min
			log.Printf("New best method: %s with chi-square: %.12e", method, result.Min)
		}
	}

	if bestResult.Status == "" {
		log.Printf("All methods failed")
		return goimpcore.Result{
			Status: "ERROR",
			Min:    math.Inf(1),
			Params: []float64{},
		}
	}

	log.Printf("Best overall result: chi-square=%.12e", bestResult.Min)
	return bestResult
}

// generateInitialValues creates reasonable default initial values for different circuit codes
func (s *Server) generateInitialValues(code string) []float64 {
	switch strings.ToLower(code) {
	case "r(cr)":
		// R1, C1, R2
		return []float64{50.0, 1e-6, 100.0}
	case "r(qr)":
		// R1, Q1_Y0, Q1_n, R2
		return []float64{50.0, 1e-6, 0.8, 100.0}
	case "r(cr)(cr)":
		// R1, C1, R2, C2, R3 (5 parameters)
		return []float64{50.0, 1e-6, 100.0, 1e-6, 100.0}
	case "r(q(r(qr)))":
		// R1, Q1_Y0, Q1_n, R2, Q2_Y0, Q2_n, R3
		return []float64{50.0, 1e-6, 0.8, 100.0, 1e-6, 0.8, 100.0}
	case "r(q(r(q(r(qr)))))":
		// R1, Q1_Y0, Q1_n, R2, Q2_Y0, Q2_n, R3, Q3_Y0, Q3_n, R4
		return []float64{50.0, 1e-6, 0.8, 100.0, 1e-6, 0.8, 100.0, 1e-6, 0.8, 100.0}
	default:
		// Generic fallback: assume 4 parameters for R(QR) since that's our default
		log.Printf("Warning: Unknown circuit code '%s', using R(QR) 4-parameter defaults", code)
		return []float64{50.0, 1e-6, 0.8, 100.0}
	}
}

// healthHandler provides a simple health check endpoint
func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"healthy","timestamp":"%s"}`, time.Now().Format(time.RFC3339))
}

// gcHandler triggers garbage collection and returns stats
func (s *Server) gcHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	profiling.ForceGC()
	stats := profiling.GetGCStats()

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{
		"gc_runs": %d,
		"pause_total_ms": %.3f,
		"pause_recent_us": %.3f,
		"cpu_percent": %.2f,
		"last_gc": "%s",
		"timestamp": "%s"
	}`,
		stats.NumGC,
		float64(stats.PauseTotal.Nanoseconds())/1000000.0,
		float64(stats.PauseRecent.Nanoseconds())/1000.0,
		stats.GCCPUPercent,
		stats.LastGC.Format(time.RFC3339),
		time.Now().Format(time.RFC3339))
}

// memoryHandler provides current memory statistics
func (s *Server) memoryHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	profiling.LogGCStats()

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"message":"Memory stats logged to console","timestamp":"%s"}`,
		time.Now().Format(time.RFC3339))
}

// Start starts the HTTP server
func (s *Server) Start() error {
	// Start profiling server
	if err := s.profiler.Start(); err != nil {
		log.Printf("❌ Failed to start profiler: %v", err)
	}

	log.Println("🚀 Starting HTTP server on port", s.serverConfig.Port)
	log.Println("📡 Endpoints available:")
	log.Printf("  - Single: http://localhost:%s/eis-data", s.serverConfig.Port)
	log.Printf("  - Batch:  http://localhost:%s/eis-data/batch", s.serverConfig.Port)
	log.Printf("  - Health: http://localhost:%s/health", s.serverConfig.Port)
	log.Printf("  - GC:     http://localhost:%s/debug/gc", s.serverConfig.Port)
	log.Printf("  - Memory: http://localhost:%s/debug/memory", s.serverConfig.Port)

	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown() error {
	log.Println("🛑 Shutting down server...")

	// Shutdown profiler
	if err := s.profiler.Stop(); err != nil {
		log.Printf("⚠️ Profiler shutdown error: %v", err)
	}

	// Shutdown worker pool
	s.workerPool.Shutdown()

	// TODO: Shutdown HTTP server gracefully
	log.Println("✅ Server shutdown complete")
	return nil
}
