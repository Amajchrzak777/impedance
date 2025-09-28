package worker

import (
	"sync"
	"testing"
	"time"

	"github.com/kacperjurak/goimpcore"
	"github.com/kacperjurak/goimpcore/pkg/config"
	"github.com/kacperjurak/goimpcore/pkg/models"
	"github.com/kacperjurak/goimpcore/pkg/webhook"
)

// mockProcessor simulates EIS processing with configurable delay
func mockProcessor(delay time.Duration) ProcessorFunc {
	return func(freqs []float64, impData [][2]float64, cfg *config.Config) interface{} {
		time.Sleep(delay) // Simulate processing time
		return goimpcore.Result{
			Status: goimpcore.OK,
			Min:    0.12345,
			Params: []float64{100.0, 0.001, 1.0, 200.0},
		}
	}
}

// createMockJob creates a test job
func createMockJob(iteration int) models.WorkItem {
	return models.WorkItem{
		ID:        iteration, // ID is int type
		RequestID: "test_request",
		BatchID:   "test_batch",
		Iteration: iteration,
		Freqs:     []float64{1000, 2000, 3000},
		ImpData:   [][2]float64{{100, -50}, {200, -100}, {300, -150}},
		Config: &config.Config{
			Code:        "r(qr)",
			OptimMethod: "nelder-mead",
		},
	}
}

// TestWorkerPoolDeadlock reproduces the deadlock scenario
func TestWorkerPoolDeadlock(t *testing.T) {
	t.Run("DeadlockWith600Jobs", func(t *testing.T) {
		// Create pool with same configuration as production (50 workers)
		pool := New(Options{
			Workers:       50,
			Processor:     mockProcessor(10 * time.Millisecond), // Fast processing
			WebhookClient: &webhook.Client{},                    // Mock webhook client
		})
		defer pool.Shutdown()

		// Submit 600 jobs quickly (like batch handler does)
		numJobs := 600
		startTime := time.Now()

		// Submit all jobs rapidly
		for i := 0; i < numJobs; i++ {
			job := createMockJob(i)
			go pool.SubmitJob(job) // Submit in parallel to speed up
		}

		t.Logf("Submitted %d jobs in %v", numJobs, time.Since(startTime))

		// Simulate batch handler result collection pattern
		resultsReceived := 0
		collectionStartTime := time.Now()
		maxWaitTime := 30 * time.Second
		resultTimeout := time.Second * 5

		for resultsReceived < numJobs {
			if time.Since(collectionStartTime) > maxWaitTime {
				t.Errorf("DEADLOCK DETECTED: Only received %d/%d results in %v",
					resultsReceived, numJobs, maxWaitTime)
				break
			}

			lastResultTime := time.Now()
			resultFound := false

			// Mimic batch handler's result collection loop
			for time.Since(lastResultTime) < resultTimeout {
				if _, ok := pool.GetResult(); ok {
					resultsReceived++
					resultFound = true

					if resultsReceived%100 == 0 {
						t.Logf("Received %d/%d results", resultsReceived, numJobs)
					}
					break
				} else {
					// Same delay as batch handler
					time.Sleep(10 * time.Millisecond)
				}
			}

			if !resultFound {
				t.Logf("WARNING: No result received within %v, progress: %d/%d",
					resultTimeout, resultsReceived, numJobs)
			}
		}

		totalTime := time.Since(collectionStartTime)
		if resultsReceived == numJobs {
			t.Logf("SUCCESS: All %d results collected in %v", resultsReceived, totalTime)
		} else {
			t.Errorf("DEADLOCK: Only %d/%d results collected", resultsReceived, numJobs)
		}
	})
}

// TestWorkerPoolChannelSaturation tests channel saturation behavior
func TestWorkerPoolChannelSaturation(t *testing.T) {
	t.Run("ResultChannelSaturation", func(t *testing.T) {
		// Create pool with small queues to trigger saturation quickly
		pool := New(Options{
			Workers:       10,
			Processor:     mockProcessor(50 * time.Millisecond), // Slower processing
			WebhookClient: &webhook.Client{},
		})
		defer pool.Shutdown()

		// Check actual queue sizes
		jobsCapacity := cap(pool.jobs)
		resultsCapacity := cap(pool.results)
		t.Logf("Queue capacities - Jobs: %d, Results: %d", jobsCapacity, resultsCapacity)

		// Submit jobs equal to result queue capacity + workers to trigger blocking
		jobsToSubmit := resultsCapacity + pool.workers + 10

		// Submit jobs and measure blocking behavior
		var submitWg sync.WaitGroup
		workerBlockedChan := make(chan int, pool.workers)

		for i := 0; i < jobsToSubmit; i++ {
			submitWg.Add(1)
			go func(jobId int) {
				defer submitWg.Done()
				startSubmit := time.Now()

				job := createMockJob(jobId)
				pool.SubmitJob(job)

				submitDuration := time.Since(startSubmit)
				if submitDuration > 100*time.Millisecond {
					workerBlockedChan <- jobId
					t.Logf("Job %d submission took %v (blocked)", jobId, submitDuration)
				}
			}(i)
		}

		// Let some jobs process
		time.Sleep(200 * time.Millisecond)

		// Count how many jobs were blocked
		close(workerBlockedChan)
		blockedJobs := 0
		for range workerBlockedChan {
			blockedJobs++
		}

		t.Logf("Blocked job submissions: %d/%d", blockedJobs, jobsToSubmit)

		// Try to collect results without consuming all
		resultsCollected := 0
		for i := 0; i < resultsCapacity/2; i++ { // Only collect half
			if _, ok := pool.GetResult(); ok {
				resultsCollected++
			} else {
				break
			}
		}

		t.Logf("Results collected: %d (intentionally partial)", resultsCollected)

		// Wait for remaining jobs to complete
		submitWg.Wait()

		// Verify if workers are blocked by checking remaining results
		remainingResults := 0
		for {
			if _, ok := pool.GetResult(); ok {
				remainingResults++
			} else {
				break
			}
		}

		t.Logf("Remaining results after partial collection: %d", remainingResults)

		if blockedJobs > 0 && remainingResults > resultsCapacity {
			t.Errorf("DEADLOCK CONFIRMED: %d jobs blocked, %d results backlogged",
				blockedJobs, remainingResults)
		}
	})
}

// TestDeadlockPrevention tests potential fixes
func TestDeadlockPrevention(t *testing.T) {
	t.Run("NonBlockingResultWrite", func(t *testing.T) {
		// Test modification to make result writes non-blocking
		pool := New(Options{
			Workers:       20,
			Processor:     mockProcessor(5 * time.Millisecond),
			WebhookClient: &webhook.Client{},
		})
		defer pool.Shutdown()

		// Create a modified worker that doesn't block on result writes
		testWorkerNonBlocking := func(id int, jobs <-chan models.WorkItem, results chan<- models.WorkResult) {
			for job := range jobs {
				processedResult := pool.processJob(job)

				// Non-blocking result write with drop behavior
				select {
				case results <- processedResult:
					// Success
				default:
					t.Logf("Worker %d: Result queue full, result dropped for job %d", id, job.Iteration)
				}
			}
		}

		// Create separate channels for this test
		testJobs := make(chan models.WorkItem, 100)
		testResults := make(chan models.WorkResult, 40) // Small result queue

		// Start test workers
		for i := 0; i < 20; i++ {
			go testWorkerNonBlocking(i, testJobs, testResults)
		}

		// Submit many jobs
		numJobs := 100
		for i := 0; i < numJobs; i++ {
			testJobs <- createMockJob(i)
		}
		close(testJobs)

		// Collect results slowly to test non-blocking behavior
		resultsCollected := 0
		for i := 0; i < numJobs; i++ {
			select {
			case <-testResults:
				resultsCollected++
			case <-time.After(100 * time.Millisecond):
				break
			}
		}

		t.Logf("Non-blocking test: Collected %d/%d results", resultsCollected, numJobs)

		// Verify system didn't deadlock (may have dropped results)
		if resultsCollected == 0 {
			t.Error("No results collected - possible deadlock")
		}
	})
}

// BenchmarkWorkerPoolThroughput benchmarks the worker pool performance
func BenchmarkWorkerPoolThroughput(b *testing.B) {
	pool := New(Options{
		Workers:       50,
		Processor:     mockProcessor(1 * time.Millisecond),
		WebhookClient: &webhook.Client{},
	})
	defer pool.Shutdown()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		job := createMockJob(i)
		pool.SubmitJob(job)

		// Immediately try to get result
		if _, ok := pool.GetResult(); !ok {
			// Result not ready yet
			time.Sleep(2 * time.Millisecond)
			pool.GetResult() // Try again
		}
	}
}
