package profiling

import (
	"fmt"
	"log"
	"runtime"
	"time"
)

// WorkerProfiler profiles worker pool operations
type WorkerProfiler struct {
	startTime   time.Time
	startMemory uint64
	workerID    int
	operation   string
}

// NewWorkerProfiler creates a new worker profiler
func NewWorkerProfiler(workerID int, operation string) *WorkerProfiler {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return &WorkerProfiler{
		startTime:   time.Now(),
		startMemory: m.Alloc,
		workerID:    workerID,
		operation:   operation,
	}
}

// Finish completes worker profiling and logs metrics
func (wp *WorkerProfiler) Finish() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	duration := time.Since(wp.startTime)
	memoryDelta := int64(m.Alloc) - int64(wp.startMemory)

	log.Printf("🔍 Worker[%d] %s: %.3fms, memory: %+d bytes, goroutines: %d",
		wp.workerID, wp.operation, float64(duration.Nanoseconds())/1000000.0, memoryDelta, runtime.NumGoroutine())
}

// WebhookProfiler profiles webhook operations
type WebhookProfiler struct {
	startTime time.Time
	requestID string
}

// NewWebhookProfiler creates a new webhook profiler
func NewWebhookProfiler(requestID string) *WebhookProfiler {
	return &WebhookProfiler{
		startTime: time.Now(),
		requestID: requestID,
	}
}

// Finish completes webhook profiling
func (whp *WebhookProfiler) Finish(success bool) {
	duration := time.Since(whp.startTime)
	status := "✅"
	if !success {
		status = "❌"
	}

	log.Printf("🌐 Webhook[%s] %s: %.3fms", whp.requestID, status, float64(duration.Nanoseconds())/1000000.0)
}

// MemoryProfiler tracks memory usage over time
type MemoryProfiler struct {
	interval time.Duration
	stopChan chan bool
}

// NewMemoryProfiler creates a new memory profiler
func NewMemoryProfiler(interval time.Duration) *MemoryProfiler {
	return &MemoryProfiler{
		interval: interval,
		stopChan: make(chan bool),
	}
}

// Start begins memory profiling
func (mp *MemoryProfiler) Start() {
	go func() {
		ticker := time.NewTicker(mp.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				mp.logMemoryStats()
			case <-mp.stopChan:
				return
			}
		}
	}()
}

// Stop ends memory profiling
func (mp *MemoryProfiler) Stop() {
	close(mp.stopChan)
}

// logMemoryStats logs current memory statistics
func (mp *MemoryProfiler) logMemoryStats() {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	log.Printf("📊 Memory: Alloc=%.2fMB, TotalAlloc=%.2fMB, Sys=%.2fMB, GC=%d, Goroutines=%d",
		bToMb(m.Alloc), bToMb(m.TotalAlloc), bToMb(m.Sys), m.NumGC, runtime.NumGoroutine())
}

// ProfileFunc profiles a function execution
func ProfileFunc(name string, fn func()) {
	profiler := NewRequestProfiler(name)
	fn()
	metrics := profiler.Finish()

	log.Printf("⚡ %s: %.3fms, memory: +%d bytes, goroutines: %d",
		metrics.Name,
		float64(metrics.Duration.Nanoseconds())/1000000.0,
		metrics.MemoryAllocated,
		metrics.Goroutines)
}

// ProfileAsyncFunc profiles an asynchronous function
func ProfileAsyncFunc(name string, fn func()) {
	go func() {
		ProfileFunc(fmt.Sprintf("async-%s", name), fn)
	}()
}

// GCStats provides garbage collection statistics
type GCStats struct {
	NumGC        uint32
	PauseTotal   time.Duration
	PauseRecent  time.Duration
	LastGC       time.Time
	GCCPUPercent float64
}

// GetGCStats returns current garbage collection statistics
func GetGCStats() GCStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	var recentPause time.Duration
	if m.NumGC > 0 {
		recentPause = time.Duration(m.PauseNs[(m.NumGC+255)%256])
	}

	return GCStats{
		NumGC:        m.NumGC,
		PauseTotal:   time.Duration(m.PauseTotalNs),
		PauseRecent:  recentPause,
		LastGC:       time.Unix(0, int64(m.LastGC)),
		GCCPUPercent: m.GCCPUFraction * 100,
	}
}

// LogGCStats logs garbage collection statistics
func LogGCStats() {
	stats := GetGCStats()
	log.Printf("🗑️  GC: Runs=%d, TotalPause=%.2fms, RecentPause=%.2fμs, CPU=%.2f%%, LastGC=%s",
		stats.NumGC,
		float64(stats.PauseTotal.Nanoseconds())/1000000.0,
		float64(stats.PauseRecent.Nanoseconds())/1000.0,
		stats.GCCPUPercent,
		stats.LastGC.Format("15:04:05"))
}

// ForceGC triggers garbage collection and logs statistics
func ForceGC() {
	before := GetGCStats()
	runtime.GC()
	after := GetGCStats()

	log.Printf("🗑️  Forced GC: %d→%d runs, pause: %.2fμs",
		before.NumGC, after.NumGC,
		float64(after.PauseRecent.Nanoseconds())/1000.0)
}
