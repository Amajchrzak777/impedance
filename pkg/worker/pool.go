package worker

import (
	"log"
	"sync"
	"time"

	"github.com/kacperjurak/goimpcore"
	"github.com/kacperjurak/goimpcore/pkg/config"
	"github.com/kacperjurak/goimpcore/pkg/models"
)

// Pool manages concurrent EIS processing workers
type Pool struct {
	jobs         chan models.WorkItem
	results      chan models.WorkResult
	webhookQueue chan models.WebhookItem
	workers      int
	bufferPool   sync.Pool
	shutdown     chan struct{}
	wg           sync.WaitGroup
	processor    ProcessorFunc
}

// ProcessorFunc defines the signature for EIS data processing
type ProcessorFunc func(freqs []float64, impData [][2]float64, config *config.Config) interface{}

// Options holds configuration for creating a new worker pool
type Options struct {
	Workers   int
	Processor ProcessorFunc
}

// New creates a new worker pool with specified configuration
func New(opts Options) *Pool {
	if opts.Workers <= 0 {
		opts.Workers = 5
	}

	// do not block queueing new jobs, and results even if the workers are already busy jobs/results * 2
	pool := &Pool{
		jobs:         make(chan models.WorkItem, opts.Workers*2),
		results:      make(chan models.WorkResult, opts.Workers*2),
		webhookQueue: make(chan models.WebhookItem, opts.Workers*4), // 4x buffer for async webhooks - possibly slower operation, that's why extended buffer
		workers:      opts.Workers,
		shutdown:     make(chan struct{}),
		processor:    opts.Processor,
		bufferPool: sync.Pool{
			New: func() interface{} {
				// Enhanced buffer pooling with larger initial capacity
				// Based on typical EIS data sizes (10-1000 frequency points)
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

// start initializes and starts all workers
func (p *Pool) start() {
	// Start processing workers
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}

	// Start webhook processor
	p.wg.Add(1)
	go p.webhookProcessor()

	log.Printf("🔧 Worker pool started with %d workers", p.workers)
}

// worker processes EIS jobs from the jobs channel
func (p *Pool) worker(id int) {
	defer p.wg.Done()

	for {
		select {
		case job := <-p.jobs:
			result := p.processJob(job)
			p.results <- result

		case <-p.shutdown:
			return
		}
	}
}

// processJob handles the actual EIS processing with buffer reuse
func (p *Pool) processJob(job models.WorkItem) models.WorkResult {
	// Get buffer from pool
	buffers := p.bufferPool.Get().(*models.BufferSet)
	defer p.bufferPool.Put(buffers)

	// Reset buffers
	buffers.Real = buffers.Real[:0]
	buffers.Imag = buffers.Imag[:0]

	// Process EIS data
	startTime := time.Now()
	log.Printf("DEBUG: About to call processor with %d frequencies, config: %+v", len(job.Freqs), job.Config.(*config.Config))
	result := p.processor(job.Freqs, job.ImpData, job.Config.(*config.Config))
	processingTime := time.Since(startTime)
	log.Printf("DEBUG: Processor returned result type: %T, value: %+v", result, result)

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
		// Fallback for invalid result
		eisResult = goimpcore.Result{
			Status: "ERROR",
			Min:    0.0,
			Params: []float64{},
		}
	}

	return models.WorkResult{
		ID:             job.ID,
		RequestID:      job.RequestID,
		BatchID:        job.BatchID,
		Iteration:      job.Iteration,
		Result:         eisResult,
		ProcessingTime: processingTime,
		Success:        eisResult.Status == goimpcore.OK,
		Freqs:          job.Freqs,
		RealImp:        realCopy,
		ImagImp:        imagCopy,
		CircuitCode:    job.Config.(*config.Config).Code,
	}
}

// extractImpedanceData extracts real and imaginary parts from impedance data
// Enhanced for better memory efficiency and reduced allocations
func (p *Pool) extractImpedanceData(impData [][2]float64, buffers *models.BufferSet) {
	dataLen := len(impData)

	// Only reallocate if current capacity is significantly smaller
	// This reduces frequent reallocations for varying data sizes
	if cap(buffers.Real) < dataLen {
		// Allocate with some extra capacity to handle size variations
		newCap := dataLen + (dataLen >> 2) // +25% extra capacity
		if newCap < 200 {
			newCap = 200 // Minimum reasonable capacity
		}
		buffers.Real = make([]float64, dataLen, newCap)
		buffers.Imag = make([]float64, dataLen, newCap)
	} else {
		buffers.Real = buffers.Real[:dataLen]
		buffers.Imag = buffers.Imag[:dataLen]
	}

	// Use range-based loop for better performance
	for i, imp := range impData {
		buffers.Real[i] = imp[0]
		buffers.Imag[i] = imp[1]
	}
}

// webhookProcessor handles webhook requests asynchronously
func (p *Pool) webhookProcessor() {
	defer p.wg.Done()

	for {
		select {
		case webhook := <-p.webhookQueue:
			// Process webhook asynchronously without blocking workers
			go p.sendWebhook(webhook)

		case <-p.shutdown:
			return
		}
	}
}

// sendWebhook is a placeholder for webhook sending logic
func (p *Pool) sendWebhook(webhook models.WebhookItem) {
	// This will be moved to the webhook package
	log.Printf("Processing webhook for %s", webhook.RequestID)
}

// SubmitJob submits a job to the worker pool
func (p *Pool) SubmitJob(job models.WorkItem) {
	select {
	case p.jobs <- job:
		// Job submitted successfully
	default:
		log.Printf("⚠️  Worker pool jobs channel full, job may be delayed")
		p.jobs <- job // Block until space available
	}
}

// GetResult retrieves a result from the worker pool (non-blocking)
func (p *Pool) GetResult() (models.WorkResult, bool) {
	select {
	case result := <-p.results:
		return result, true
	default:
		return models.WorkResult{}, false
	}
}

// QueueWebhook queues a webhook for async processing
func (p *Pool) QueueWebhook(webhook models.WebhookItem) {
	select {
	case p.webhookQueue <- webhook:
		// Webhook queued successfully
	default:
		log.Printf("⚠️  Webhook queue full, dropping webhook for %s", webhook.RequestID)
	}
}

// Shutdown gracefully shuts down the worker pool
func (p *Pool) Shutdown() {
	log.Printf("🛑 Shutting down worker pool...")
	close(p.shutdown)
	p.wg.Wait()
	log.Printf("✅ Worker pool shutdown complete")
}
