#!/bin/bash

# GoImpCore Profiling Test Suite
# Automated profiling and bottleneck analysis based on PROFILING_GUIDE.md
# Tests various aspects: CPU, Memory, Goroutines, Webhooks, and Worker Pool efficiency

set -e

# Configuration
SERVER_DIR="/Users/adammajchrzak/ghq/github.com/adam/masterapp/goimpcore"
RESULTS_DIR="$SERVER_DIR/profiling_results"
TEST_DATA="$SERVER_DIR/test_batch_profiling.json"
PROFILES_DIR="$RESULTS_DIR/profiles"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
NC='\033[0m' # No Color

# Logging functions
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

log_section() {
    echo -e "\n${PURPLE}========== $1 ==========${NC}"
}

# Setup function
setup_profiling_test() {
    log_section "Setting Up Profiling Test Environment"
    
    # Create results directory
    mkdir -p "$RESULTS_DIR" "$PROFILES_DIR"
    
    # Build the server
    cd "$SERVER_DIR/cmd/goimpsolver-restructured"
    if ! go build; then
        log_error "Failed to build server"
        exit 1
    fi
    
    log_success "Server built successfully"
    
    # Verify test data exists
    if [ ! -f "$TEST_DATA" ]; then
        log_error "Test data file not found: $TEST_DATA"
        exit 1
    fi
    
    log_success "Test data verified: $TEST_DATA"
}

# Function to start server with profiling
start_server_with_profiling() {
    local threads=${1:-4}
    local profile_enabled=${2:-true}
    
    log_info "Starting server with $threads threads, profiling=$profile_enabled"
    
    # Kill any existing server
    pkill -f goimpsolver-restructured || true
    sleep 2
    
    cd "$SERVER_DIR/cmd/goimpsolver-restructured"
    
    if [ "$profile_enabled" = "true" ]; then
        ./goimpsolver-restructured -server -threads=$threads -profile &
    else
        ./goimpsolver-restructured -server -threads=$threads &
    fi
    
    SERVER_PID=$!
    
    # Wait for server to start
    local attempts=0
    while [ $attempts -lt 10 ]; do
        if curl -s http://localhost:8080/health >/dev/null 2>&1; then
            log_success "Server started with PID $SERVER_PID"
            return 0
        fi
        sleep 1
        ((attempts++))
    done
    
    log_error "Failed to start server after 10 attempts"
    return 1
}

# Function to stop server
stop_server() {
    if [ -n "$SERVER_PID" ]; then
        kill $SERVER_PID 2>/dev/null || true
        wait $SERVER_PID 2>/dev/null || true
        log_info "Server stopped"
    fi
}

# CPU Profiling Test
cpu_profiling_test() {
    log_section "CPU Profiling Test"
    
    local test_duration=30
    local profile_file="$PROFILES_DIR/cpu_profile_$(date +%Y%m%d_%H%M%S).pprof"
    
    # Start CPU profiling in background
    log_info "Starting CPU profile collection for ${test_duration}s..."
    curl -s "http://localhost:6060/debug/pprof/profile?seconds=$test_duration" > "$profile_file" &
    PROFILE_PID=$!
    
    # Generate load during profiling
    log_info "Generating load during CPU profiling..."
    for i in {1..5}; do
        curl -X POST -H "Content-Type: application/json" \
            -d @"$TEST_DATA" \
            http://localhost:8080/eis-data/batch >/dev/null 2>&1 &
    done
    
    # Wait for profiling to complete
    wait $PROFILE_PID
    
    if [ -f "$profile_file" ] && [ -s "$profile_file" ]; then
        log_success "CPU profile saved: $profile_file"
        
        # Analyze CPU profile
        log_info "Analyzing CPU profile..."
        echo "Top 10 CPU consumers:" > "$PROFILES_DIR/cpu_analysis.txt"
        go tool pprof -text -lines -nodecount=10 "$profile_file" >> "$PROFILES_DIR/cpu_analysis.txt" 2>/dev/null || true
        
        log_success "CPU analysis saved to cpu_analysis.txt"
    else
        log_error "Failed to collect CPU profile"
    fi
}

# Memory Profiling Test
memory_profiling_test() {
    log_section "Memory Profiling Test"
    
    local heap_before="$PROFILES_DIR/heap_before_$(date +%Y%m%d_%H%M%S).pprof"
    local heap_after="$PROFILES_DIR/heap_after_$(date +%Y%m%d_%H%M%S).pprof"
    
    # Take initial heap snapshot
    log_info "Taking initial heap snapshot..."
    curl -s "http://localhost:6060/debug/pprof/heap" > "$heap_before"
    
    # Generate memory-intensive workload
    log_info "Running memory-intensive workload..."
    for i in {1..10}; do
        curl -X POST -H "Content-Type: application/json" \
            -d @"$TEST_DATA" \
            http://localhost:8080/eis-data/batch >/dev/null 2>&1
        sleep 1
    done
    
    # Take final heap snapshot
    log_info "Taking final heap snapshot..."
    curl -s "http://localhost:6060/debug/pprof/heap" > "$heap_after"
    
    # Analyze memory usage
    if [ -f "$heap_before" ] && [ -f "$heap_after" ]; then
        log_info "Analyzing memory usage..."
        echo "Memory Usage Analysis:" > "$PROFILES_DIR/memory_analysis.txt"
        echo "=====================" >> "$PROFILES_DIR/memory_analysis.txt"
        echo "" >> "$PROFILES_DIR/memory_analysis.txt"
        
        echo "Memory growth during test:" >> "$PROFILES_DIR/memory_analysis.txt"
        go tool pprof -text -base "$heap_before" "$heap_after" >> "$PROFILES_DIR/memory_analysis.txt" 2>/dev/null || true
        
        log_success "Memory analysis saved to memory_analysis.txt"
    else
        log_error "Failed to collect memory profiles"
    fi
}

# Goroutine Analysis Test
goroutine_analysis_test() {
    log_section "Goroutine Analysis Test"
    
    local goroutine_file="$PROFILES_DIR/goroutines_$(date +%Y%m%d_%H%M%S).txt"
    
    # Generate concurrent load
    log_info "Generating concurrent load for goroutine analysis..."
    for i in {1..20}; do
        curl -X POST -H "Content-Type: application/json" \
            -d @"$TEST_DATA" \
            http://localhost:8080/eis-data/batch >/dev/null 2>&1 &
    done
    
    # Wait a moment for goroutines to spawn
    sleep 3
    
    # Capture goroutine information
    log_info "Capturing goroutine information..."
    curl -s "http://localhost:6060/debug/pprof/goroutine?debug=1" > "$goroutine_file"
    
    # Analyze goroutines
    if [ -f "$goroutine_file" ] && [ -s "$goroutine_file" ]; then
        local goroutine_count=$(grep -c "goroutine " "$goroutine_file")
        log_success "Captured $goroutine_count goroutines: $goroutine_file"
        
        # Create goroutine summary
        echo "Goroutine Analysis Summary:" > "$PROFILES_DIR/goroutine_summary.txt"
        echo "==========================" >> "$PROFILES_DIR/goroutine_summary.txt"
        echo "Total goroutines: $goroutine_count" >> "$PROFILES_DIR/goroutine_summary.txt"
        echo "" >> "$PROFILES_DIR/goroutine_summary.txt"
        echo "Goroutine states:" >> "$PROFILES_DIR/goroutine_summary.txt"
        grep -o '\[.*\]' "$goroutine_file" | sort | uniq -c >> "$PROFILES_DIR/goroutine_summary.txt"
        
        log_success "Goroutine summary saved"
    else
        log_error "Failed to capture goroutine information"
    fi
    
    # Wait for background jobs to finish
    wait
}

# Runtime Statistics Collection
runtime_stats_test() {
    log_section "Runtime Statistics Collection"
    
    local stats_file="$PROFILES_DIR/runtime_stats_$(date +%Y%m%d_%H%M%S).json"
    local info_file="$PROFILES_DIR/runtime_info_$(date +%Y%m%d_%H%M%S).json"
    
    # Collect runtime info
    log_info "Collecting runtime information..."
    curl -s "http://localhost:6060/debug/info" > "$info_file"
    
    # Collect runtime stats (this takes ~30s)
    log_info "Collecting runtime statistics (30s)..."
    curl -s "http://localhost:6060/debug/stats" > "$stats_file" &
    STATS_PID=$!
    
    # Generate load during stats collection
    for i in {1..15}; do
        curl -X POST -H "Content-Type: application/json" \
            -d @"$TEST_DATA" \
            http://localhost:8080/eis-data/batch >/dev/null 2>&1
        sleep 2
    done
    
    wait $STATS_PID
    
    if [ -f "$stats_file" ] && [ -s "$stats_file" ]; then
        log_success "Runtime stats collected: $stats_file"
    fi
    
    if [ -f "$info_file" ] && [ -s "$info_file" ]; then
        log_success "Runtime info collected: $info_file"
    fi
}

# GC Analysis Test
gc_analysis_test() {
    log_section "Garbage Collection Analysis"
    
    local gc_file="$PROFILES_DIR/gc_analysis_$(date +%Y%m%d_%H%M%S).txt"
    
    log_info "Triggering GC and collecting statistics..."
    
    # Collect initial GC stats
    echo "GC Analysis - $(date)" > "$gc_file"
    echo "=================" >> "$gc_file"
    echo "" >> "$gc_file"
    
    # Trigger GC and collect stats
    curl -s "http://localhost:8080/debug/gc" >> "$gc_file"
    
    log_success "GC analysis saved: $gc_file"
}

# Performance Benchmarking
performance_benchmark_test() {
    log_section "Performance Benchmarking"
    
    local benchmark_file="$PROFILES_DIR/performance_benchmark_$(date +%Y%m%d_%H%M%S).csv"
    
    echo "threads,concurrent_requests,avg_response_time_ms,total_time_ms,requests_per_second" > "$benchmark_file"
    
    # Test different thread counts
    for threads in 2 4 8 16; do
        log_info "Testing performance with $threads threads..."
        
        # Restart server with new thread count
        stop_server
        sleep 2
        start_server_with_profiling $threads true
        sleep 3
        
        # Run benchmark
        local concurrent_requests=10
        local start_time=$(date +%s%3N)
        
        for i in $(seq 1 $concurrent_requests); do
            curl -X POST -H "Content-Type: application/json" \
                -d @"$TEST_DATA" \
                http://localhost:8080/eis-data/batch >/dev/null 2>&1 &
        done
        
        wait
        local end_time=$(date +%s%3N)
        local total_time=$((end_time - start_time))
        local avg_response_time=$((total_time / concurrent_requests))
        local requests_per_second=$(echo "scale=2; 1000 * $concurrent_requests / $total_time" | bc -l)
        
        echo "$threads,$concurrent_requests,$avg_response_time,$total_time,$requests_per_second" >> "$benchmark_file"
        
        log_success "Benchmark for $threads threads: ${requests_per_second} req/s"
    done
    
    log_success "Performance benchmark saved: $benchmark_file"
}

# Bottleneck Analysis
analyze_bottlenecks() {
    log_section "Bottleneck Analysis"
    
    local analysis_file="$PROFILES_DIR/bottleneck_analysis_$(date +%Y%m%d_%H%M%S).md"
    
    cat > "$analysis_file" << EOF
# GoImpCore Bottleneck Analysis Report
Generated: $(date)

## Summary
This report analyzes performance bottlenecks based on profiling data collected.

## CPU Analysis
EOF
    
    if [ -f "$PROFILES_DIR/cpu_analysis.txt" ]; then
        echo "### Top CPU Consumers" >> "$analysis_file"
        echo '```' >> "$analysis_file"
        head -n 20 "$PROFILES_DIR/cpu_analysis.txt" >> "$analysis_file"
        echo '```' >> "$analysis_file"
    fi
    
    cat >> "$analysis_file" << EOF

## Memory Analysis
EOF
    
    if [ -f "$PROFILES_DIR/memory_analysis.txt" ]; then
        echo "### Memory Usage Patterns" >> "$analysis_file"
        echo '```' >> "$analysis_file"
        head -n 20 "$PROFILES_DIR/memory_analysis.txt" >> "$analysis_file"
        echo '```' >> "$analysis_file"
    fi
    
    cat >> "$analysis_file" << EOF

## Goroutine Analysis
EOF
    
    if [ -f "$PROFILES_DIR/goroutine_summary.txt" ]; then
        echo "### Goroutine Summary" >> "$analysis_file"
        echo '```' >> "$analysis_file"
        cat "$PROFILES_DIR/goroutine_summary.txt" >> "$analysis_file"
        echo '```' >> "$analysis_file"
    fi
    
    cat >> "$analysis_file" << EOF

## Performance Benchmark Results
EOF
    
    if [ -f "$PROFILES_DIR/performance_benchmark"*.csv ]; then
        echo "### Throughput Analysis" >> "$analysis_file"
        echo '```' >> "$analysis_file"
        cat "$PROFILES_DIR/performance_benchmark"*.csv >> "$analysis_file"
        echo '```' >> "$analysis_file"
    fi
    
    cat >> "$analysis_file" << EOF

## Recommendations

### CPU Optimization
- Review functions with highest CPU usage from profile
- Consider optimizing hot paths in EIS processing
- Evaluate algorithmic complexity of bottleneck functions

### Memory Optimization
- Monitor for memory leaks in worker pool
- Review large object allocations
- Consider implementing object pooling for frequently allocated objects

### Concurrency Optimization
- Analyze goroutine lifecycle and cleanup
- Review channel usage and potential deadlocks
- Optimize worker pool sizing based on CPU cores

### System Tuning
- Adjust GOGC environment variable if needed
- Consider thread count based on performance benchmarks
- Monitor webhook queue sizing for high-throughput scenarios
EOF
    
    log_success "Bottleneck analysis report saved: $analysis_file"
}

# Cleanup function
cleanup() {
    log_section "Cleaning Up"
    stop_server
    
    # Kill any remaining background processes
    jobs -p | xargs -r kill 2>/dev/null || true
    
    log_success "Cleanup completed"
}

# Main function
main() {
    log_section "GoImpCore Profiling Test Suite"
    log_info "Starting comprehensive profiling and bottleneck analysis"
    
    # Set trap for cleanup
    trap cleanup EXIT INT TERM
    
    # Setup
    setup_profiling_test
    
    # Start server with profiling enabled
    start_server_with_profiling 4 true
    
    # Wait for server to be fully ready
    sleep 5
    
    # Run all profiling tests
    cpu_profiling_test
    memory_profiling_test
    goroutine_analysis_test
    runtime_stats_test
    gc_analysis_test
    performance_benchmark_test
    
    # Analyze results
    analyze_bottlenecks
    
    log_section "Profiling Test Suite Complete"
    log_success "All results saved to: $RESULTS_DIR"
    log_info "Review the bottleneck analysis report for optimization recommendations"
    
    # Show directory contents
    echo ""
    echo "Generated files:"
    ls -la "$PROFILES_DIR/"
}

# Check dependencies
check_dependencies() {
    local deps=("curl" "go" "bc" "jq")
    for dep in "${deps[@]}"; do
        if ! command -v "$dep" >/dev/null 2>&1; then
            log_error "Required dependency not found: $dep"
            exit 1
        fi
    done
}

# Entry point
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    check_dependencies
    main "$@"
fi