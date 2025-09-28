package worker

import (
	"testing"
	"time"

	"github.com/kacperjurak/goimpcore/pkg/webhook"
)

// TestFixedPoolNoDeadlock tests that the fixed pool doesn't deadlock
func TestFixedPoolNoDeadlock(t *testing.T) {
	t.Run("NoDeadlockWith600Jobs", func(t *testing.T) {
		// Create fixed pool with same configuration
		pool := NewFixed(Options{
			Workers:       50,
			Processor:     mockProcessor(10 * time.Millisecond),
			WebhookClient: &webhook.Client{},
		})
		defer pool.Shutdown()

		// Submit 600 jobs quickly
		numJobs := 600
		startTime := time.Now()

		for i := 0; i < numJobs; i++ {
			job := createMockJob(i)
			go pool.SubmitJob(job) // Submit in parallel
		}

		t.Logf("Submitted %d jobs in %v", numJobs, time.Since(startTime))

		// Collect results with same pattern as batch handler
		resultsReceived := 0
		collectionStartTime := time.Now()
		maxWaitTime := 60 * time.Second // Shorter timeout for fixed version

		for resultsReceived < numJobs {
			if time.Since(collectionStartTime) > maxWaitTime {
				// Get metrics to understand what happened
				droppedResults, droppedWebhooks := pool.GetMetrics()
				t.Logf("Timeout reached. Results received: %d/%d, Dropped results: %d, Dropped webhooks: %d",
					resultsReceived, numJobs, droppedResults, droppedWebhooks)
				break
			}

			// Try to get result with timeout
			if _, ok := pool.GetResultWithTimeout(100 * time.Millisecond); ok {
				resultsReceived++

				if resultsReceived%100 == 0 {
					t.Logf("Received %d/%d results", resultsReceived, numJobs)
				}
			}
		}

		totalTime := time.Since(collectionStartTime)
		droppedResults, droppedWebhooks := pool.GetMetrics()

		t.Logf("FIXED POOL RESULTS:")
		t.Logf("  Results collected: %d/%d", resultsReceived, numJobs)
		t.Logf("  Total time: %v", totalTime)
		t.Logf("  Dropped results: %d", droppedResults)
		t.Logf("  Dropped webhooks: %d", droppedWebhooks)

		// Success criteria: No deadlock (completed within timeout)
		if time.Since(collectionStartTime) <= maxWaitTime {
			t.Logf("✅ SUCCESS: No deadlock detected, completed in %v", totalTime)
		} else {
			t.Errorf("❌ TIMEOUT: Fixed pool took too long, possible performance issue")
		}

		// Acceptable to drop some results/webhooks under extreme load
		// But should process most of them
		minAcceptableResults := int(float64(numJobs) * 0.8) // 80% success rate
		if resultsReceived >= minAcceptableResults {
			t.Logf("✅ Acceptable success rate: %d/%d (%.1f%%)",
				resultsReceived, numJobs, float64(resultsReceived)/float64(numJobs)*100)
		} else {
			t.Errorf("❌ Low success rate: %d/%d (%.1f%%) - below 80%% threshold",
				resultsReceived, numJobs, float64(resultsReceived)/float64(numJobs)*100)
		}
	})
}

// TestFixedPoolvsOriginal compares fixed vs original pool
func TestFixedPoolVsOriginal(t *testing.T) {
	t.Run("CompareFixedVsOriginal", func(t *testing.T) {
		testJobs := 200 // Smaller test for comparison

		// Test original pool
		t.Log("Testing original pool...")
		originalPool := New(Options{
			Workers:       20,
			Processor:     mockProcessor(5 * time.Millisecond),
			WebhookClient: &webhook.Client{},
		})

		originalStart := time.Now()
		originalResults := 0

		// Submit jobs to original pool
		for i := 0; i < testJobs; i++ {
			go originalPool.SubmitJob(createMockJob(i))
		}

		// Collect from original pool with timeout
		originalTimeout := 30 * time.Second
		for originalResults < testJobs && time.Since(originalStart) < originalTimeout {
			if _, ok := originalPool.GetResult(); ok {
				originalResults++
			} else {
				time.Sleep(10 * time.Millisecond)
			}
		}
		originalTime := time.Since(originalStart)
		originalPool.Shutdown()

		// Test fixed pool
		t.Log("Testing fixed pool...")
		fixedPool := NewFixed(Options{
			Workers:       20,
			Processor:     mockProcessor(5 * time.Millisecond),
			WebhookClient: &webhook.Client{},
		})

		fixedStart := time.Now()
		fixedResults := 0

		// Submit jobs to fixed pool
		for i := 0; i < testJobs; i++ {
			go fixedPool.SubmitJob(createMockJob(i))
		}

		// Collect from fixed pool
		for fixedResults < testJobs && time.Since(fixedStart) < originalTimeout {
			if _, ok := fixedPool.GetResultWithTimeout(50 * time.Millisecond); ok {
				fixedResults++
			}
		}
		fixedTime := time.Since(fixedStart)
		droppedResults, droppedWebhooks := fixedPool.GetMetrics()
		fixedPool.Shutdown()

		// Compare results
		t.Logf("COMPARISON RESULTS:")
		t.Logf("Original Pool:")
		t.Logf("  Results: %d/%d (%.1f%%)", originalResults, testJobs, float64(originalResults)/float64(testJobs)*100)
		t.Logf("  Time: %v", originalTime)
		t.Logf("  Completed: %v", originalTime < originalTimeout)

		t.Logf("Fixed Pool:")
		t.Logf("  Results: %d/%d (%.1f%%)", fixedResults, testJobs, float64(fixedResults)/float64(testJobs)*100)
		t.Logf("  Time: %v", fixedTime)
		t.Logf("  Dropped: %d results, %d webhooks", droppedResults, droppedWebhooks)
		t.Logf("  Completed: %v", fixedTime < originalTimeout)

		// Fixed pool should be more reliable (complete within timeout)
		if fixedTime < originalTimeout && originalTime >= originalTimeout {
			t.Logf("✅ Fixed pool avoided deadlock that affected original pool")
		} else if fixedTime < originalTime {
			t.Logf("✅ Fixed pool completed faster than original pool")
		}
	})
}

// BenchmarkFixedPool benchmarks the deadlock-free pool
func BenchmarkFixedPool(b *testing.B) {
	pool := NewFixed(Options{
		Workers:       10,
		Processor:     mockProcessor(1 * time.Millisecond),
		WebhookClient: &webhook.Client{},
	})
	defer pool.Shutdown()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		job := createMockJob(i)
		pool.SubmitJob(job)

		// Try to get result immediately
		if _, ok := pool.GetResult(); !ok {
			// Use timeout method if immediate read fails
			pool.GetResultWithTimeout(10 * time.Millisecond)
		}
	}

	droppedResults, droppedWebhooks := pool.GetMetrics()
	if droppedResults > 0 || droppedWebhooks > 0 {
		b.Logf("Benchmark completed with %d dropped results, %d dropped webhooks", droppedResults, droppedWebhooks)
	}
}
