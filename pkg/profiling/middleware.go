package profiling

import (
	"net/http"
	"runtime"
	"strconv"
	"time"
)

// Middleware provides profiling and metrics middleware for HTTP handlers
type Middleware struct {
	enableProfiling bool
}

// NewMiddleware creates a new profiling middleware
func NewMiddleware(enableProfiling bool) *Middleware {
	return &Middleware{
		enableProfiling: enableProfiling,
	}
}

// ProfiledHandler wraps an HTTP handler with profiling capabilities
func (m *Middleware) ProfiledHandler(name string, handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.enableProfiling {
			handler.ServeHTTP(w, r)
			return
		}

		// Capture initial state
		startTime := time.Now()
		var startMemStats runtime.MemStats
		runtime.ReadMemStats(&startMemStats)
		startGoroutines := runtime.NumGoroutine()

		// Add profiling headers
		w.Header().Set("X-Profiling-Enabled", "true")
		w.Header().Set("X-Handler-Name", name)
		w.Header().Set("X-Start-Time", startTime.Format(time.RFC3339Nano))
		w.Header().Set("X-Start-Goroutines", strconv.Itoa(startGoroutines))

		// Wrap response writer to capture status
		wrapped := &responseWriter{
			ResponseWriter: w,
			statusCode:     200,
		}

		// Execute handler
		handler.ServeHTTP(wrapped, r)

		// Capture final state
		endTime := time.Now()
		var endMemStats runtime.MemStats
		runtime.ReadMemStats(&endMemStats)
		endGoroutines := runtime.NumGoroutine()

		// Calculate metrics
		duration := endTime.Sub(startTime)
		memoryDelta := int64(endMemStats.Alloc) - int64(startMemStats.Alloc)
		goroutineDelta := endGoroutines - startGoroutines

		// Add performance headers
		wrapped.Header().Set("X-Duration-Ms", strconv.FormatFloat(float64(duration.Nanoseconds())/1000000.0, 'f', 3, 64))
		wrapped.Header().Set("X-Memory-Delta-Bytes", strconv.FormatInt(memoryDelta, 10))
		wrapped.Header().Set("X-Goroutine-Delta", strconv.Itoa(goroutineDelta))
		wrapped.Header().Set("X-End-Goroutines", strconv.Itoa(endGoroutines))
		wrapped.Header().Set("X-Status-Code", strconv.Itoa(wrapped.statusCode))
		wrapped.Header().Set("X-Profiling-Complete", "true")
	})
}

// ProfiledHandlerFunc wraps an HTTP handler function with profiling capabilities
func (m *Middleware) ProfiledHandlerFunc(name string, handlerFunc http.HandlerFunc) http.Handler {
	return m.ProfiledHandler(name, handlerFunc)
}

// responseWriter wraps http.ResponseWriter to capture the status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	return rw.ResponseWriter.Write(b)
}

// RequestProfiler provides per-request profiling information
type RequestProfiler struct {
	StartTime   time.Time
	StartMemory uint64
	Name        string
}

// NewRequestProfiler creates a new request profiler
func NewRequestProfiler(name string) *RequestProfiler {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return &RequestProfiler{
		StartTime:   time.Now(),
		StartMemory: m.Alloc,
		Name:        name,
	}
}

// Finish completes the profiling and returns metrics
func (rp *RequestProfiler) Finish() ProfileMetrics {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return ProfileMetrics{
		Name:            rp.Name,
		Duration:        time.Since(rp.StartTime),
		MemoryAllocated: m.Alloc - rp.StartMemory,
		FinalMemory:     m.Alloc,
		Goroutines:      runtime.NumGoroutine(),
	}
}

// ProfileMetrics holds profiling metrics for a request
type ProfileMetrics struct {
	Name            string
	Duration        time.Duration
	MemoryAllocated uint64
	FinalMemory     uint64
	Goroutines      int
}
