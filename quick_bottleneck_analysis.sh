#!/bin/bash

# Quick Bottleneck Analysis for GoImpCore
# A simplified version for rapid bottleneck identification

set -e

# Configuration
SERVER_DIR="/Users/adammajchrzak/ghq/github.com/adam/masterapp/goimpcore"
TEST_DATA="$SERVER_DIR/test_batch_profiling.json"

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
RED='\033[0;31m'
NC='\033[0m'

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if server is running with profiling
check_server() {
    log_info "Checking server status..."
    
    # Check if main server is running
    if ! curl -s http://localhost:8080/health >/dev/null 2>&1; then
        log_error "Server not running on port 8080"
        log_info "Start server with: ./goimpsolver-restructured -server -threads=4 -profile"
        exit 1
    fi
    
    # Check if profiling is enabled
    if ! curl -s http://localhost:6060/debug/pprof/ >/dev/null 2>&1; then
        log_warning "Profiling not enabled on port 6060"
        log_info "Restart server with -profile flag for full analysis"
        return 1
    fi
    
    log_success "Server running with profiling enabled"
    return 0
}

# Quick CPU analysis
quick_cpu_analysis() {
    log_info "Running 10-second CPU analysis..."
    
    # Start CPU profiling
    curl -s "http://localhost:6060/debug/pprof/profile?seconds=10" > cpu_quick.pprof &
    PROFILE_PID=$!
    
    # Generate some load
    curl -X POST -H "Content-Type: application/json" -d @"$TEST_DATA" http://localhost:8080/eis-data/batch >/dev/null 2>&1 &
    curl -X POST -H "Content-Type: application/json" -d @"$TEST_DATA" http://localhost:8080/eis-data/batch >/dev/null 2>&1 &
    
    wait $PROFILE_PID
    
    if [ -f "cpu_quick.pprof" ] && [ -s "cpu_quick.pprof" ]; then
        log_success "CPU profile collected"
        echo ""
        echo "=== TOP CPU CONSUMERS ==="
        go tool pprof -text -lines -nodecount=5 cpu_quick.pprof 2>/dev/null | head -n 15
        rm cpu_quick.pprof
    else
        log_error "Failed to collect CPU profile"
    fi
}

# Quick memory analysis
quick_memory_analysis() {
    log_info "Collecting memory snapshot..."
    
    if curl -s "http://localhost:6060/debug/pprof/heap" > heap_quick.pprof; then
        log_success "Memory profile collected"
        echo ""
        echo "=== MEMORY USAGE ==="
        go tool pprof -text -nodecount=5 heap_quick.pprof 2>/dev/null | head -n 15
        rm heap_quick.pprof
    else
        log_error "Failed to collect memory profile"
    fi
}

# Quick goroutine analysis
quick_goroutine_analysis() {
    log_info "Analyzing current goroutines..."
    
    # Generate some concurrent load
    for i in {1..5}; do
        curl -X POST -H "Content-Type: application/json" -d @"$TEST_DATA" http://localhost:8080/eis-data/batch >/dev/null 2>&1 &
    done
    
    sleep 2
    
    # Get goroutine info
    local goroutines=$(curl -s "http://localhost:6060/debug/pprof/goroutine?debug=1")
    local count=$(echo "$goroutines" | grep -c "goroutine " || echo "0")
    
    echo ""
    echo "=== GOROUTINE ANALYSIS ==="
    echo "Total goroutines: $count"
    
    if [ $count -gt 50 ]; then
        log_warning "High goroutine count detected ($count)"
    elif [ $count -gt 100 ]; then
        log_error "Very high goroutine count detected ($count) - potential leak"
    else
        log_success "Normal goroutine count ($count)"
    fi
    
    echo ""
    echo "Goroutine states:"
    echo "$goroutines" | grep -o '\[.*\]' | sort | uniq -c | head -n 10
    
    wait # Wait for background jobs
}

# Quick runtime stats
quick_runtime_stats() {
    log_info "Collecting runtime statistics..."
    
    local runtime_info=$(curl -s "http://localhost:6060/debug/info" 2>/dev/null)
    
    if [ -n "$runtime_info" ]; then
        echo ""
        echo "=== RUNTIME STATISTICS ==="
        echo "$runtime_info" | jq -r '
            "Go Version: " + .go_version,
            "OS/Arch: " + .os + "/" + .arch,
            "CPUs: " + (.num_cpu | tostring),
            "Goroutines: " + (.goroutines | tostring),
            "CGO Calls: " + (.cgo_calls | tostring)
        ' 2>/dev/null || echo "$runtime_info"
    fi
    
    # Get GC stats
    log_info "Checking GC statistics..."
    local gc_stats=$(curl -s "http://localhost:8080/debug/gc" 2>/dev/null)
    if [ -n "$gc_stats" ]; then
        echo ""
        echo "=== GC STATISTICS ==="
        echo "$gc_stats" | jq -r '
            "GC Runs: " + (.num_gc | tostring),
            "Total Pause: " + (.pause_total_ns | tostring) + "ns",
            "CPU Fraction: " + ((.gc_cpu_fraction * 100 | . * 100 | round / 100) | tostring) + "%"
        ' 2>/dev/null || echo "$gc_stats"
    fi
}

# Performance test
quick_performance_test() {
    log_info "Running quick performance test..."
    
    local start_time=$(date +%s)
    local requests=5
    
    echo "Sending $requests concurrent requests..."
    for i in $(seq 1 $requests); do
        curl -X POST -H "Content-Type: application/json" -d @"$TEST_DATA" http://localhost:8080/eis-data/batch >/dev/null 2>&1 &
    done
    
    wait
    local end_time=$(date +%s)
    local total_time=$((end_time - start_time))
    local avg_response_time_sec=$total_time
    local requests_per_second=$(echo "scale=2; $requests / $total_time" | bc -l 2>/dev/null || echo "N/A")
    
    echo ""
    echo "=== PERFORMANCE RESULTS ==="
    echo "Total time: ${total_time}s"
    echo "Average response time: ${avg_response_time_sec}s"
    echo "Throughput: ${requests_per_second} req/s"
    
    if [ $total_time -lt 5 ]; then
        log_success "Good performance (${total_time}s total)"
    elif [ $total_time -lt 15 ]; then
        log_warning "Moderate performance (${total_time}s total)"
    else
        log_error "Poor performance (${total_time}s total)"
    fi
}

# Summary and recommendations
provide_recommendations() {
    echo ""
    echo "=== QUICK RECOMMENDATIONS ==="
    echo "1. CPU: Check top functions for optimization opportunities"
    echo "2. Memory: Monitor heap growth and GC frequency"
    echo "3. Goroutines: Ensure proper cleanup and avoid leaks"
    echo "4. Performance: Consider thread tuning based on CPU count"
    echo ""
    echo "For detailed analysis, run: ./profiling_test.sh"
}

# Main function
main() {
    echo "GoImpCore Quick Bottleneck Analysis"
    echo "==================================="
    
    # Change to server directory
    cd "$SERVER_DIR"
    
    # Check if server is running
    if ! check_server; then
        log_info "Running analysis without profiling data..."
    fi
    
    # Run quick tests
    quick_runtime_stats
    quick_performance_test
    
    # Run profiling tests if available
    if curl -s http://localhost:6060/debug/pprof/ >/dev/null 2>&1; then
        quick_cpu_analysis
        quick_memory_analysis
        quick_goroutine_analysis
    fi
    
    provide_recommendations
}

# Check dependencies
if ! command -v curl >/dev/null 2>&1; then
    log_error "curl is required but not installed"
    exit 1
fi

if ! command -v jq >/dev/null 2>&1; then
    log_warning "jq not found - JSON output will be raw"
fi

# Run main function
main "$@"