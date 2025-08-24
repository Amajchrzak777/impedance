package profiling

import (
	"context"
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof" // Import pprof handlers
	"runtime"
	"time"

	"github.com/kacperjurak/goimpcore/pkg/config"
)

// Profiler manages pprof profiling server
type Profiler struct {
	config *config.ServerConfig
	server *http.Server
}

// New creates a new profiler instance
func New(cfg *config.ServerConfig) *Profiler {
	return &Profiler{
		config: cfg,
	}
}

// Start starts the profiling server on a separate port
func (p *Profiler) Start() error {
	if !p.config.EnableProfiling {
		log.Println("📊 Profiling disabled")
		return nil
	}

	// Enable more detailed profiling
	runtime.SetBlockProfileRate(1)
	runtime.SetMutexProfileFraction(1)

	// Create profiling server with custom routes
	mux := http.NewServeMux()

	// Default pprof endpoints are automatically registered at import
	// Add custom profiling endpoints
	mux.HandleFunc("/debug/pprof/", http.DefaultServeMux.ServeHTTP)
	mux.HandleFunc("/debug/pprof/cmdline", http.DefaultServeMux.ServeHTTP)
	mux.HandleFunc("/debug/pprof/profile", http.DefaultServeMux.ServeHTTP)
	mux.HandleFunc("/debug/pprof/symbol", http.DefaultServeMux.ServeHTTP)
	mux.HandleFunc("/debug/pprof/trace", http.DefaultServeMux.ServeHTTP)

	// Add custom profiling info endpoint
	mux.HandleFunc("/debug/info", p.infoHandler)
	mux.HandleFunc("/debug/stats", p.statsHandler)

	p.server = &http.Server{
		Addr:    ":" + p.config.ProfilingPort,
		Handler: mux,
	}

	log.Printf("📊 Starting profiling server on port %s", p.config.ProfilingPort)
	log.Printf("📈 Profiling endpoints:")
	log.Printf("  - CPU Profile:    http://localhost:%s/debug/pprof/profile", p.config.ProfilingPort)
	log.Printf("  - Heap Profile:   http://localhost:%s/debug/pprof/heap", p.config.ProfilingPort)
	log.Printf("  - Goroutines:     http://localhost:%s/debug/pprof/goroutine", p.config.ProfilingPort)
	log.Printf("  - Block Profile:  http://localhost:%s/debug/pprof/block", p.config.ProfilingPort)
	log.Printf("  - Mutex Profile:  http://localhost:%s/debug/pprof/mutex", p.config.ProfilingPort)
	log.Printf("  - Full Index:     http://localhost:%s/debug/pprof/", p.config.ProfilingPort)
	log.Printf("  - Runtime Info:   http://localhost:%s/debug/info", p.config.ProfilingPort)
	log.Printf("  - Runtime Stats:  http://localhost:%s/debug/stats", p.config.ProfilingPort)

	// Start server in goroutine
	go func() {
		if err := p.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("❌ Profiling server error: %v", err)
		}
	}()

	return nil
}

// Stop gracefully stops the profiling server
func (p *Profiler) Stop() error {
	if p.server == nil {
		return nil
	}

	log.Println("🛑 Shutting down profiling server...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := p.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("profiling server shutdown error: %w", err)
	}

	log.Println("✅ Profiling server stopped")
	return nil
}

// infoHandler provides runtime information
func (p *Profiler) infoHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	info := map[string]interface{}{
		"timestamp":  time.Now().Format(time.RFC3339),
		"goroutines": runtime.NumGoroutine(),
		"gomaxprocs": runtime.GOMAXPROCS(0),
		"num_cpu":    runtime.NumCPU(),
		"version":    runtime.Version(),
		"memory": map[string]interface{}{
			"alloc_mb":        bToMb(m.Alloc),
			"total_alloc_mb":  bToMb(m.TotalAlloc),
			"sys_mb":          bToMb(m.Sys),
			"heap_alloc_mb":   bToMb(m.HeapAlloc),
			"heap_sys_mb":     bToMb(m.HeapSys),
			"heap_objects":    m.HeapObjects,
			"stack_in_use_mb": bToMb(m.StackInuse),
			"stack_sys_mb":    bToMb(m.StackSys),
		},
		"gc": map[string]interface{}{
			"num_gc":         m.NumGC,
			"pause_total_ns": m.PauseTotalNs,
			"last_gc":        time.Unix(0, int64(m.LastGC)).Format(time.RFC3339),
		},
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{
  "timestamp": "%s",
  "goroutines": %d,
  "gomaxprocs": %d,
  "num_cpu": %d,
  "version": "%s",
  "memory": {
    "alloc_mb": %.2f,
    "total_alloc_mb": %.2f,
    "sys_mb": %.2f,
    "heap_alloc_mb": %.2f,
    "heap_sys_mb": %.2f,
    "heap_objects": %d,
    "stack_in_use_mb": %.2f,
    "stack_sys_mb": %.2f
  },
  "gc": {
    "num_gc": %d,
    "pause_total_ns": %d,
    "last_gc": "%s"
  }
}`, info["timestamp"], info["goroutines"], info["gomaxprocs"], info["num_cpu"], info["version"],
		info["memory"].(map[string]interface{})["alloc_mb"],
		info["memory"].(map[string]interface{})["total_alloc_mb"],
		info["memory"].(map[string]interface{})["sys_mb"],
		info["memory"].(map[string]interface{})["heap_alloc_mb"],
		info["memory"].(map[string]interface{})["heap_sys_mb"],
		info["memory"].(map[string]interface{})["heap_objects"],
		info["memory"].(map[string]interface{})["stack_in_use_mb"],
		info["memory"].(map[string]interface{})["stack_sys_mb"],
		info["gc"].(map[string]interface{})["num_gc"],
		info["gc"].(map[string]interface{})["pause_total_ns"],
		info["gc"].(map[string]interface{})["last_gc"])
}

// statsHandler provides continuous runtime statistics
func (p *Profiler) statsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)

	// Print runtime statistics every second for 30 seconds
	for i := 0; i < 30; i++ {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)

		fmt.Fprintf(w, "=== Runtime Stats [%02d] ===\n", i+1)
		fmt.Fprintf(w, "Timestamp: %s\n", time.Now().Format("15:04:05"))
		fmt.Fprintf(w, "Goroutines: %d\n", runtime.NumGoroutine())
		fmt.Fprintf(w, "Memory Allocated: %.2f MB\n", bToMb(m.Alloc))
		fmt.Fprintf(w, "Total Allocations: %.2f MB\n", bToMb(m.TotalAlloc))
		fmt.Fprintf(w, "System Memory: %.2f MB\n", bToMb(m.Sys))
		fmt.Fprintf(w, "GC Runs: %d\n", m.NumGC)
		fmt.Fprintf(w, "Heap Objects: %d\n", m.HeapObjects)
		fmt.Fprintf(w, "\n")

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		time.Sleep(1 * time.Second)
	}
}

// bToMb converts bytes to megabytes
func bToMb(b uint64) float64 {
	return float64(b) / 1024 / 1024
}
