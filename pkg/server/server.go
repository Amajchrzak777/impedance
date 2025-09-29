package server

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/kacperjurak/goimpcore/internal/processing"
	"github.com/kacperjurak/goimpcore/pkg/config"
	"github.com/kacperjurak/goimpcore/pkg/handlers"
	"github.com/kacperjurak/goimpcore/pkg/profiling"
	"github.com/kacperjurak/goimpcore/pkg/webhook"
	"github.com/kacperjurak/goimpcore/pkg/worker"
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
	eisProcessor  *processing.EISProcessor
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

	// Create webhook client
	webhookClient := webhook.NewClient(opts.ServerConfig.WebhookURL, opts.Config)

	// Create worker pool with webhook client
	workerPool := worker.New(worker.Options{
		Workers:       opts.ServerConfig.WorkerCount,
		Processor:     worker.ProcessorFunc(opts.Processor),
		WebhookClient: webhookClient,
	})

	// Create profiler and middleware
	profiler := profiling.New(opts.ServerConfig)
	middleware := profiling.NewMiddleware(opts.ServerConfig.EnableProfiling)

	// Create EIS processor
	eisProcessor := processing.NewEISProcessor()

	// Create HTTP server
	server := &Server{
		config:        opts.Config,
		serverConfig:  opts.ServerConfig,
		workerPool:    workerPool,
		webhookClient: webhookClient,
		profiler:      profiler,
		middleware:    middleware,
		eisProcessor:  eisProcessor,
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
// CRITICAL FIX: Create a new EIS processor instance for each call to ensure thread safety
// The shared s.eisProcessor was causing race conditions between workers
func (s *Server) getProcessorFunc() handlers.ProcessorFunc {
	return func(freqs []float64, impData [][2]float64, cfg *config.Config) interface{} {
		// Create a NEW processor instance for each call - this ensures thread safety
		processor := processing.NewEISProcessor()
		result, err := processor.Process(freqs, impData, cfg)
		if err != nil {
			log.Printf("EIS processing error: %v", err)
			return result // Return the error result from processor
		}
		return result
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
