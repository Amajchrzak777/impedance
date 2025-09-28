package worker

import (
	"log"
	"sync"
	"time"

	"github.com/kacperjurak/goimpcore"
	"github.com/kacperjurak/goimpcore/pkg/config"
	"github.com/kacperjurak/goimpcore/pkg/models"
	"github.com/kacperjurak/goimpcore/pkg/webhook"
)

// FixedPool is a deadlock-free version of the worker pool
type FixedPool struct {
	jobs          chan models.WorkItem
	results       chan models.WorkResult
	webhookQueue  chan models.WebhookItem
	workers       int
	bufferPool    sync.Pool
	shutdown      chan struct{}
	wg            sync.WaitGroup
	processor     ProcessorFunc
	webhookClient *webhook.Client

	// Deadlock prevention: metrics and monitoring
	droppedResults  int64
	droppedWebhooks int64
	metricsLock     sync.RWMutex
}

// NewFixed creates a deadlock-free worker pool
func NewFixed(opts Options) *FixedPool {
	if opts.Workers <= 0 {
		opts.Workers = 5
	}

	// Conservative queue sizes to prevent memory issues
	jobQueueSize := opts.Workers * 2     // Smaller job buffer
	resultQueueSize := opts.Workers * 3  // Moderate result buffer
	webhookQueueSize := opts.Workers * 4 // Larger webhook buffer

	pool := &FixedPool{
		jobs:          make(chan models.WorkItem, jobQueueSize),
		results:       make(chan models.WorkResult, resultQueueSize),
		webhookQueue:  make(chan models.WebhookItem, webhookQueueSize),
		workers:       opts.Workers,
		shutdown:      make(chan struct{}),
		processor:     opts.Processor,
		webhookClient: opts.WebhookClient,
		bufferPool: sync.Pool{
			New: func() interface{} {
				return &models.BufferSet{
					Real: make([]float64, 0, 200),
					Imag: make([]float64, 0, 200),
					Imp:  make([][2]float64, 0, 200),
				}
			},
		},
	}

	pool.start()
	return pool
}

// start initializes workers with deadlock prevention
func (p *FixedPool) start() {
	// Start processing workers
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.workerFixed(i)
	}

	// Start webhook processor
	p.wg.Add(1)
	go p.webhookProcessor()

	log.Printf("🔧 Fixed worker pool started with %d workers", p.workers)
	log.Printf("📊 Conservative queue sizes - Jobs: %d, Results: %d, Webhooks: %d",
		cap(p.jobs), cap(p.results), cap(p.webhookQueue))
}

// workerFixed is the deadlock-free worker implementation
func (p *FixedPool) workerFixed(id int) {
	defer p.wg.Done()

	for {
		select {
		case job := <-p.jobs:
			result := p.processJob(job)

			// DEADLOCK FIX: Non-blocking result write with timeout
			select {
			case p.results <- result:
				// Result sent successfully
			case <-time.After(100 * time.Millisecond):
				// Result channel full, drop with warning
				p.metricsLock.Lock()
				p.droppedResults++
				dropped := p.droppedResults
				p.metricsLock.Unlock()

				log.Printf("⚠️ Worker %d: Result queue full, dropped result for job %d (total dropped: %d)",
					id, job.Iteration, dropped)
			}

		case <-p.shutdown:
			return
		}
	}
}

// processJob is the same as original but with enhanced logging
func (p *FixedPool) processJob(job models.WorkItem) models.WorkResult {
	// Get buffer from pool
	buffers := p.bufferPool.Get().(*models.BufferSet)
	defer p.bufferPool.Put(buffers)

	// Reset buffers
	buffers.Real = buffers.Real[:0]
	buffers.Imag = buffers.Imag[:0]

	// Process EIS data
	startTime := time.Now()
	result := p.processor(job.Freqs, job.ImpData, job.Config.(*config.Config))
	processingTime := time.Since(startTime)

	// Extract impedance data with pre-allocated buffers
	p.extractImpedanceData(job.ImpData, buffers)

	// Create copies for result (buffers will be reused)
	realCopy := make([]float64, len(buffers.Real))
	imagCopy := make([]float64, len(buffers.Imag))
	copy(realCopy, buffers.Real)
	copy(imagCopy, buffers.Imag)

	// Type assert result to goimpcore.Result
	eisResult, ok := result.(goimpcore.Result)
	if !ok {
		eisResult = goimpcore.Result{
			Status: "ERROR",
			Min:    0.0,
			Params: []float64{},
		}
	}

	// Copy parameters
	var parameters []float64
	if eisResult.Status == goimpcore.OK && len(eisResult.Params) > 0 {
		parameters = make([]float64, len(eisResult.Params))
		copy(parameters, eisResult.Params)
	}

	return models.WorkResult{
		ID:                 job.ID,
		RequestID:          job.RequestID,
		BatchID:            job.BatchID,
		Iteration:          job.Iteration,
		Result:             eisResult,
		ProcessingTime:     processingTime,
		Success:            eisResult.Status == goimpcore.OK,
		Freqs:              job.Freqs,
		RealImp:            realCopy,
		ImagImp:            imagCopy,
		CircuitCode:        job.Config.(*config.Config).Code,
		OptimizationMethod: job.Config.(*config.Config).OptimMethod,
		Parameters:         parameters,
	}
}

// extractImpedanceData is same as original
func (p *FixedPool) extractImpedanceData(impData [][2]float64, buffers *models.BufferSet) {
	dataLen := len(impData)

	if cap(buffers.Real) < dataLen {
		newCap := dataLen + (dataLen >> 2)
		if newCap < 200 {
			newCap = 200
		}
		buffers.Real = make([]float64, dataLen, newCap)
		buffers.Imag = make([]float64, dataLen, newCap)
	} else {
		buffers.Real = buffers.Real[:dataLen]
		buffers.Imag = buffers.Imag[:dataLen]
	}

	for i, imp := range impData {
		buffers.Real[i] = imp[0]
		buffers.Imag[i] = imp[1]
	}
}

// webhookProcessor with deadlock prevention
func (p *FixedPool) webhookProcessor() {
	defer p.wg.Done()

	for {
		select {
		case webhook := <-p.webhookQueue:
			// Process webhook asynchronously without blocking
			go p.sendWebhook(webhook)

		case <-p.shutdown:
			return
		}
	}
}

// sendWebhook same as original
func (p *FixedPool) sendWebhook(webhook models.WebhookItem) {
	if err := p.webhookClient.Send(webhook); err != nil {
		log.Printf("Failed to send webhook for %s: %v", webhook.RequestID, err)
	}
}

// SubmitJob with enhanced monitoring
func (p *FixedPool) SubmitJob(job models.WorkItem) {
	select {
	case p.jobs <- job:
		// Job submitted successfully
	default:
		// DEADLOCK PREVENTION: Log but don't block indefinitely
		log.Printf("⚠️ Job queue full, blocking for job %d", job.Iteration)

		// Block with timeout to prevent infinite blocking
		select {
		case p.jobs <- job:
			log.Printf("✅ Job %d submitted after delay", job.Iteration)
		case <-time.After(5 * time.Second):
			log.Printf("❌ Job %d submission timeout - system overloaded", job.Iteration)
		}
	}
}

// GetResult enhanced with metrics
func (p *FixedPool) GetResult() (models.WorkResult, bool) {
	select {
	case result := <-p.results:
		return result, true
	default:
		return models.WorkResult{}, false
	}
}

// GetResultWithTimeout provides blocking read with timeout
func (p *FixedPool) GetResultWithTimeout(timeout time.Duration) (models.WorkResult, bool) {
	select {
	case result := <-p.results:
		return result, true
	case <-time.After(timeout):
		return models.WorkResult{}, false
	}
}

// QueueWebhook with deadlock prevention
func (p *FixedPool) QueueWebhook(webhook models.WebhookItem) {
	select {
	case p.webhookQueue <- webhook:
		// Webhook queued successfully
	default:
		// DEADLOCK FIX: Drop webhook instead of blocking
		p.metricsLock.Lock()
		p.droppedWebhooks++
		dropped := p.droppedWebhooks
		p.metricsLock.Unlock()

		log.Printf("⚠️ Webhook queue full, dropping webhook for %s (total dropped: %d)",
			webhook.RequestID, dropped)
	}
}

// GetMetrics returns pool performance metrics
func (p *FixedPool) GetMetrics() (droppedResults, droppedWebhooks int64) {
	p.metricsLock.RLock()
	defer p.metricsLock.RUnlock()
	return p.droppedResults, p.droppedWebhooks
}

// Shutdown gracefully shuts down the worker pool
func (p *FixedPool) Shutdown() {
	log.Printf("🛑 Shutting down fixed worker pool...")

	p.metricsLock.RLock()
	droppedResults := p.droppedResults
	droppedWebhooks := p.droppedWebhooks
	p.metricsLock.RUnlock()

	if droppedResults > 0 || droppedWebhooks > 0 {
		log.Printf("📊 Final metrics - Dropped results: %d, Dropped webhooks: %d",
			droppedResults, droppedWebhooks)
	}

	close(p.shutdown)
	p.wg.Wait()
	log.Printf("✅ Fixed worker pool shutdown complete")
}
