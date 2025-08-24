# GoImpCore - Profiling Guide

This guide explains how to use the integrated pprof profiling capabilities in the restructured GoImpCore application for performance analysis and optimization.

## 🚀 Quick Start

### Enable Profiling

```bash
# Start server with profiling enabled
./goimpsolver-restructured -server -threads=4 -profile

# Without profiling (default)
./goimpsolver-restructured -server -threads=4
```

When profiling is enabled, you'll see output like:
```
📊 Starting profiling server on port 6060
📈 Profiling endpoints:
  - CPU Profile:    http://localhost:6060/debug/pprof/profile
  - Heap Profile:   http://localhost:6060/debug/pprof/heap
  - Goroutines:     http://localhost:6060/debug/pprof/goroutine
  - Block Profile:  http://localhost:6060/debug/pprof/block
  - Mutex Profile:  http://localhost:6060/debug/pprof/mutex
  - Full Index:     http://localhost:6060/debug/pprof/
  - Runtime Info:   http://localhost:6060/debug/info
  - Runtime Stats:  http://localhost:6060/debug/stats
```

## 📊 Profiling Endpoints

### 1. Standard pprof Endpoints (Port 6060)

| Endpoint | Description | Usage |
|----------|-------------|-------|
| `/debug/pprof/` | Profile index with all available profiles | View in browser |
| `/debug/pprof/profile` | CPU profile (30s default) | `go tool pprof http://localhost:6060/debug/pprof/profile` |
| `/debug/pprof/heap` | Memory heap profile | `go tool pprof http://localhost:6060/debug/pprof/heap` |
| `/debug/pprof/goroutine` | Goroutine stack traces | `curl "http://localhost:6060/debug/pprof/goroutine?debug=1"` |
| `/debug/pprof/block` | Blocking operations profile | `go tool pprof http://localhost:6060/debug/pprof/block` |
| `/debug/pprof/mutex` | Mutex contention profile | `go tool pprof http://localhost:6060/debug/pprof/mutex` |
| `/debug/info` | Runtime information (JSON) | `curl http://localhost:6060/debug/info` |
| `/debug/stats` | Live runtime statistics (30s) | `curl http://localhost:6060/debug/stats` |

### 2. Application Debug Endpoints (Port 8080)

| Endpoint | Description | Usage |
|----------|-------------|-------|
| `/debug/gc` | Trigger GC and return statistics | `curl http://localhost:8080/debug/gc` |
| `/debug/memory` | Log memory stats to console | `curl http://localhost:8080/debug/memory` |

## 🔍 Profiling Features

### 1. HTTP Request Profiling

When profiling is enabled, HTTP responses include performance headers:

```
X-Profiling-Enabled: true
X-Handler-Name: eis-batch
X-Start-Time: 2025-08-21T22:12:04.848817+02:00
X-Start-Goroutines: 8
X-Duration-Ms: 2.145
X-Memory-Delta-Bytes: 1024
X-Goroutine-Delta: 0
X-End-Goroutines: 8
X-Status-Code: 202
```

### 2. Worker Pool Profiling

Worker operations are automatically profiled and logged:

```
🔍 Worker[0] eis-processing: 1.234ms, memory: +2048 bytes, goroutines: 9
🔍 Worker[1] eis-processing: 0.987ms, memory: +1536 bytes, goroutines: 9
```

### 3. Webhook Profiling

Webhook operations include timing information:

```
🌐 Webhook[abc123] ✅: 45.678ms
🌐 Webhook[def456] ❌: 102.345ms
```

### 4. Memory Profiling

Continuous memory monitoring available:

```
📊 Memory: Alloc=2.45MB, TotalAlloc=15.67MB, Sys=12.34MB, GC=5, Goroutines=9
```

## 🛠️ Usage Examples

### 1. CPU Profiling

```bash
# Collect 30-second CPU profile
curl "http://localhost:6060/debug/pprof/profile" > cpu.pprof

# Collect 10-second CPU profile  
curl "http://localhost:6060/debug/pprof/profile?seconds=10" > cpu.pprof

# Analyze with pprof
go tool pprof cpu.pprof
(pprof) top10
(pprof) web
```

### 2. Memory Profiling

```bash
# Collect heap profile
curl "http://localhost:6060/debug/pprof/heap" > heap.pprof

# Force GC before heap profile
curl "http://localhost:6060/debug/pprof/heap?gc=1" > heap_after_gc.pprof

# Analyze memory usage
go tool pprof heap.pprof
(pprof) top10
(pprof) list functionName
```

### 3. Goroutine Analysis

```bash
# Get goroutine dump
curl "http://localhost:6060/debug/pprof/goroutine?debug=1" > goroutines.txt

# Get full stack trace
curl "http://localhost:6060/debug/pprof/goroutine?debug=2" > full_stacks.txt
```

### 4. Real-time Monitoring

```bash
# Get current runtime info
curl http://localhost:6060/debug/info | jq

# Monitor memory stats for 30 seconds
curl http://localhost:6060/debug/stats

# Trigger GC and see stats
curl http://localhost:8080/debug/gc | jq
```

## 📈 Performance Analysis Workflow

### 1. Load Testing with Profiling

```bash
# Start server with profiling
./goimpsolver-restructured -server -threads=8 -profile

# In another terminal, start CPU profiling
curl "http://localhost:6060/debug/pprof/profile?seconds=30" > load_test_cpu.pprof &

# Run load test (using real impedance data)
for i in {1..10}; do
  curl -X POST -H "Content-Type: application/json" \
    -d @test_batch_profiling.json \
    http://localhost:8080/eis-data/batch &
done

wait  # Wait for all requests to complete

# Analyze results
go tool pprof load_test_cpu.pprof
```

### 2. Memory Leak Detection

```bash
# Take initial heap snapshot
curl "http://localhost:6060/debug/pprof/heap" > heap_before.pprof

# Run workload...
# (process many EIS batches)

# Take final heap snapshot
curl "http://localhost:6060/debug/pprof/heap" > heap_after.pprof

# Compare memory usage
go tool pprof -base heap_before.pprof heap_after.pprof
```

### 3. Concurrency Analysis

```bash
# Monitor goroutines during batch processing
watch -n 1 'curl -s "http://localhost:6060/debug/pprof/goroutine?debug=1" | head -5'

# Check for blocking operations
curl "http://localhost:6060/debug/pprof/block" > block.pprof
go tool pprof block.pprof

# Check mutex contention
curl "http://localhost:6060/debug/pprof/mutex" > mutex.pprof
go tool pprof mutex.pprof
```

## 🎯 Key Metrics to Monitor

### 1. Worker Pool Efficiency

- **Goroutine count**: Should match worker count + overhead
- **Memory per worker**: Monitor for memory leaks in worker processes
- **Processing time**: Individual EIS processing duration

### 2. Webhook Performance

- **Webhook queue depth**: Monitor queue overflow (4x buffer)
- **Webhook delivery time**: Network latency impact
- **Webhook success rate**: Failed deliveries

### 3. Overall System Health

- **GC frequency**: High GC frequency indicates memory pressure
- **Memory allocation rate**: Rapid allocation may cause GC pressure
- **CPU utilization**: Balance between workers and system resources

## 🚨 Performance Optimization Tips

### 1. Worker Pool Tuning

```bash
# Test different worker counts
./goimpsolver-restructured -server -threads=4 -profile   # 4 workers
./goimpsolver-restructured -server -threads=8 -profile   # 8 workers
./goimpsolver-restructured -server -threads=16 -profile  # 16 workers
```

### 2. Memory Optimization

- Monitor buffer pool effectiveness in worker processing
- Check for memory leaks in long-running processes
- Tune GC parameters if needed: `GOGC=100 ./goimpsolver-restructured`

### 3. Webhook Queue Sizing

The webhook queue is sized at `workers * 4` to handle:
- Network latency variations
- Webhook endpoint failures
- Burst processing scenarios

Monitor queue drops in logs:
```
⚠️  Webhook queue full, dropping webhook for abc123
```

## 📋 Troubleshooting

### Common Issues

1. **High Memory Usage**
   ```bash
   # Check for memory leaks
   go tool pprof http://localhost:6060/debug/pprof/heap
   ```

2. **Slow Processing**
   ```bash
   # Profile CPU usage
   go tool pprof http://localhost:6060/debug/pprof/profile
   ```

3. **Goroutine Leaks**
   ```bash
   # Monitor goroutine count over time
   curl "http://localhost:6060/debug/info" | jq '.goroutines'
   ```

4. **Webhook Bottlenecks**
   ```bash
   # Check webhook processing times in logs
   # Look for "Processing webhook" entries
   ```

## 🔧 Configuration

Profiling can be configured via command-line flags:

```bash
# Enable profiling (default: disabled)
-profile=true

# Custom profiling port (default: 6060)
# Note: Currently hardcoded, but can be made configurable
```

Environment variables:
```bash
# Adjust garbage collector
export GOGC=100

# Enable more detailed profiling
export GODEBUG=gctrace=1
```

This profiling setup provides comprehensive performance monitoring for the GoImpCore application, enabling detailed analysis of the asynchronous worker pool, webhook processing, and overall system performance.