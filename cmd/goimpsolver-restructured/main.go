package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/kacperjurak/goimpcore/internal/processing"
	"github.com/kacperjurak/goimpcore/pkg/config"
	"github.com/kacperjurak/goimpcore/pkg/server"
)

func main() {
	// Parse command line flags
	cfg := parseFlags()

	// Create EIS processor
	processor := processing.NewEISProcessor()

	// Create server configuration
	serverConfig := &config.ServerConfig{
		Port:            "8080",
		WorkerCount:     int(cfg.Threads),
		WebhookURL:      "http://webplot:3001/webhook",
		EnableMetrics:   true,
		EnableProfiling: cfg.EnableProfiling,
		ProfilingPort:   "6060",
	}

	// Create and start server
	srv := server.New(server.Options{
		Config:       cfg,
		ServerConfig: serverConfig,
		Processor:    processor.ProcessorFunc(),
	})

	// Setup graceful shutdown
	setupGracefulShutdown(srv)

	// Start server
	if err := srv.Start(); err != nil {
		log.Fatal("❌ Failed to start server:", err)
	}
}

// parseFlags parses command line flags and returns configuration
func parseFlags() *config.Config {
	cfg := config.DefaultConfig()

	flag.StringVar(&cfg.Code, "R(QR)", cfg.Code, "Circuit code (e.g., R(RC))")
	flag.StringVar(&cfg.File, "file", cfg.File, "Input file path")
	flag.UintVar(&cfg.Threads, "threads", cfg.Threads, "Number of worker threads")
	flag.BoolVar(&cfg.Quiet, "quiet", cfg.Quiet, "Suppress verbose output")
	flag.BoolVar(&cfg.HTTPServer, "server", cfg.HTTPServer, "Start HTTP server")
	flag.BoolVar(&cfg.Benchmark, "benchmark", cfg.Benchmark, "Enable benchmark mode")
	flag.BoolVar(&cfg.EnableProfiling, "profile", cfg.EnableProfiling, "Enable pprof profiling")
	flag.StringVar(&cfg.OptimMethod, "method", cfg.OptimMethod, "Optimization method")

	flag.Parse()

	return cfg
}

// setupGracefulShutdown sets up graceful shutdown handling
func setupGracefulShutdown(srv *server.Server) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-c
		log.Println("🛑 Received shutdown signal...")
		if err := srv.Shutdown(); err != nil {
			log.Printf("Error during shutdown: %v", err)
		}
		os.Exit(0)
	}()
}
