#!/bin/bash

# Performance Comparison Test for GoImpCore Optimizations
# Compares performance before and after bottleneck analysis improvements

set -e

# Configuration
SERVER_DIR="/Users/adammajchrzak/ghq/github.com/adam/masterapp/goimpcore"
RESULTS_DIR="$SERVER_DIR/performance_comparison_results"
TEST_DATA="$SERVER_DIR/test_batch_profiling.json"
PROFILES_DIR="$RESULTS_DIR/profiles"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
CYAN='\033[0;36m'
NC='\033[0m'

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

log_test() {
    echo -e "${CYAN}[TEST]${NC} $1"
}

# Setup function
setup_test() {
    log_section "Setting Up Performance Comparison Test"
    
    mkdir -p "$RESULTS_DIR" "$PROFILES_DIR"
    
    # Build the server
    cd "$SERVER_DIR/cmd/goimpsolver-restructured"
    if ! go build; then
        log_error "Failed to build server"
        exit 1
    fi
    
    log_success "Server built successfully"
}

# Function to start server with different configurations
start_server() {
    local config=$1
    local threads=${2:-4}
    
    log_info "Starting server with $config configuration (threads=$threads)"
    
    # Kill any existing server
    pkill -f goimpsolver-restructured || true
    sleep 2
    
    cd "$SERVER_DIR/cmd/goimpsolver-restructured"
    
    case $config in
        "baseline")
            # Standard configuration without optimizations
            GOGC=100 ./goimpsolver-restructured -server -threads=$threads -profile &
            ;;
        "optimized")
            # Use optimized startup script
            cd "$SERVER_DIR"
            ./start_optimized.sh -t $threads -p -m &
            ;;
    esac
    
    SERVER_PID=$!
    
    # Wait for server to start
    local attempts=0
    while [ $attempts -lt 15 ]; do
        if curl -s http://localhost:8080/health >/dev/null 2>&1; then
            log_success "Server started with PID $SERVER_PID ($config mode)"
            return 0
        fi
        sleep 1
        ((attempts++))
    done
    
    log_error "Failed to start server after 15 attempts"
    return 1
}

# Function to stop server
stop_server() {
    if [ -n "$SERVER_PID" ]; then
        kill $SERVER_PID 2>/dev/null || true
        wait $SERVER_PID 2>/dev/null || true
        log_info "Server stopped"
        sleep 2
    fi
}

# Performance test function
run_performance_test() {
    local config=$1
    local threads=$2
    local test_name="${config}_${threads}threads"
    local results_file="$RESULTS_DIR/${test_name}_results.json"
    
    log_test "Running performance test: $test_name"
    
    # Start server with specific configuration
    if ! start_server "$config" "$threads"; then
        log_error "Failed to start server for $test_name"
        return 1
    fi
    
    # Wait for server to fully initialize
    sleep 5
    
    # Collect initial memory stats
    local initial_memory=$(curl -s "http://localhost:6060/debug/pprof/heap" | wc -c || echo "0")
    local initial_goroutines=$(curl -s "http://localhost:6060/debug/info" | jq -r '.goroutines' 2>/dev/null || echo "0")
    
    log_info "Initial state - Memory: ${initial_memory} bytes, Goroutines: ${initial_goroutines}"
    
    # Performance test parameters
    local concurrent_requests=10
    local test_rounds=3
    local total_requests=$((concurrent_requests * test_rounds))
    
    # Array to store response times
    local response_times=()
    local successful_requests=0
    local total_time=0
    
    log_info "Running $test_rounds rounds of $concurrent_requests concurrent requests each"
    local overall_start=$(date +%s%3N 2>/dev/null || date +%s)
    
    # Run multiple rounds of concurrent requests
    for round in $(seq 1 $test_rounds); do
        log_info "Round $round/$test_rounds"
        local round_start=$(date +%s%3N 2>/dev/null || date +%s)
        
        # Launch concurrent requests
        for i in $(seq 1 $concurrent_requests); do
            {
                local req_start=$(date +%s%3N 2>/dev/null || date +%s)
                if curl -s -X POST -H "Content-Type: application/json" -d @"$TEST_DATA" http://localhost:8080/eis-data/batch >/dev/null 2>&1; then
                    local req_end=$(date +%s%3N 2>/dev/null || date +%s)
                    local req_time=$((req_end - req_start))
                    echo "$req_time" >> "$RESULTS_DIR/temp_times_${test_name}.txt"
                    ((successful_requests++))
                fi
            } &
        done
        
        # Wait for round to complete
        wait
        
        local round_end=$(date +%s%3N 2>/dev/null || date +%s)
        local round_duration=$((round_end - round_start))
        log_info "Round $round completed in ${round_duration}ms"
        
        # Brief pause between rounds
        sleep 2
    done
    
    local overall_end=$(date +%s%3N 2>/dev/null || date +%s)
    total_time=$((overall_end - overall_start))
    
    # Collect final memory stats
    local final_memory=$(curl -s "http://localhost:6060/debug/pprof/heap" | wc -c || echo "0")
    local final_goroutines=$(curl -s "http://localhost:6060/debug/info" | jq -r '.goroutines' 2>/dev/null || echo "0")
    
    # Calculate statistics
    local memory_growth=$((final_memory - initial_memory))
    local goroutine_growth=$((final_goroutines - initial_goroutines))
    
    # Process response times
    local avg_response_time=0
    local min_response_time=999999
    local max_response_time=0
    
    if [ -f "$RESULTS_DIR/temp_times_${test_name}.txt" ]; then
        while read -r time; do
            response_times+=("$time")
            if [ "$time" -lt "$min_response_time" ]; then
                min_response_time=$time
            fi
            if [ "$time" -gt "$max_response_time" ]; then
                max_response_time=$time
            fi
        done < "$RESULTS_DIR/temp_times_${test_name}.txt"
        
        # Calculate average
        local total_time=0
        for time in "${response_times[@]}"; do
            total_time=$((total_time + time))
        done
        
        if [ ${#response_times[@]} -gt 0 ]; then
            avg_response_time=$((total_time / ${#response_times[@]}))
        fi
        
        rm "$RESULTS_DIR/temp_times_${test_name}.txt"
    fi
    
    # Calculate throughput
    local throughput=$(echo "scale=2; $successful_requests / ($total_time / 1000)" | bc -l 2>/dev/null || echo "N/A")
    
    # Create results JSON
    cat > "$results_file" << EOF
{
    "test_name": "$test_name",
    "config": "$config",
    "threads": $threads,
    "timestamp": "$(date -Iseconds)",
    "performance": {
        "total_requests": $total_requests,
        "successful_requests": $successful_requests,
        "success_rate": $(echo "scale=2; $successful_requests * 100 / $total_requests" | bc -l 2>/dev/null || echo "0"),
        "avg_response_time_ms": $avg_response_time,
        "min_response_time_ms": $min_response_time,
        "max_response_time_ms": $max_response_time,
        "throughput_rps": "$throughput"
    },
    "memory": {
        "initial_heap_bytes": $initial_memory,
        "final_heap_bytes": $final_memory,
        "memory_growth_bytes": $memory_growth
    },
    "concurrency": {
        "initial_goroutines": $initial_goroutines,
        "final_goroutines": $final_goroutines,
        "goroutine_growth": $goroutine_growth
    }
}
EOF
    
    # Log results
    log_success "Test completed: $test_name"
    echo "  Successful requests: $successful_requests/$total_requests"
    echo "  Average response time: ${avg_response_time}ms"
    echo "  Throughput: $throughput req/s"
    echo "  Memory growth: $memory_growth bytes"
    echo "  Goroutine growth: $goroutine_growth"
    
    # Stop server
    stop_server
}

# Generate comparison report
generate_comparison_report() {
    log_section "Generating Performance Comparison Report"
    
    local report_file="$RESULTS_DIR/performance_comparison_report.md"
    
    cat > "$report_file" << 'EOF'
# GoImpCore Performance Comparison Report

**Generated:** $(date)
**Test Configuration:** Bottleneck analysis optimizations vs baseline

## Test Summary

This report compares performance before and after implementing the bottleneck analysis recommendations:

### Optimizations Applied:
1. **GC Tuning:** GOGC=50 for low latency, GOMEMLIMIT=2GB
2. **HTTP Connection Pooling:** Enhanced webhook client with connection reuse
3. **Object Pooling:** Improved buffer pooling with larger initial capacity (200 vs 50)
4. **Memory Management:** Enhanced buffer allocation strategies

## Results Comparison

EOF
    
    # Add results for each test
    for result_file in "$RESULTS_DIR"/*_results.json; do
        if [ -f "$result_file" ]; then
            local test_name=$(basename "$result_file" _results.json)
            echo "### $test_name" >> "$report_file"
            echo '```json' >> "$report_file"
            jq '.' "$result_file" 2>/dev/null >> "$report_file" || cat "$result_file" >> "$report_file"
            echo '```' >> "$report_file"
            echo "" >> "$report_file"
        fi
    done
    
    cat >> "$report_file" << 'EOF'

## Performance Analysis

### Key Metrics Comparison:

| Configuration | Avg Response Time | Throughput | Memory Growth | Success Rate |
|---------------|-------------------|------------|---------------|--------------|
EOF
    
    # Add comparison table rows
    for result_file in "$RESULTS_DIR"/*_results.json; do
        if [ -f "$result_file" ]; then
            local config=$(jq -r '.config' "$result_file" 2>/dev/null || echo "unknown")
            local threads=$(jq -r '.threads' "$result_file" 2>/dev/null || echo "unknown")
            local avg_time=$(jq -r '.performance.avg_response_time_ms' "$result_file" 2>/dev/null || echo "N/A")
            local throughput=$(jq -r '.performance.throughput_rps' "$result_file" 2>/dev/null || echo "N/A")
            local memory_growth=$(jq -r '.memory.memory_growth_bytes' "$result_file" 2>/dev/null || echo "N/A")
            local success_rate=$(jq -r '.performance.success_rate' "$result_file" 2>/dev/null || echo "N/A")
            
            echo "| ${config} (${threads}t) | ${avg_time}ms | ${throughput} req/s | ${memory_growth}B | ${success_rate}% |" >> "$report_file"
        fi
    done
    
    cat >> "$report_file" << 'EOF'

## Recommendations

Based on the performance comparison:

### If Optimized Configuration Shows Improvement:
- Deploy with optimized settings in production
- Monitor memory usage patterns
- Consider further GC tuning based on workload

### If Baseline Performs Better:
- Investigate specific optimization bottlenecks
- Profile individual components (webhook vs worker pool)
- Adjust buffer sizes based on actual data patterns

## Next Steps

1. Run tests with larger datasets
2. Test with different thread counts
3. Profile specific bottlenecks in detail
4. Monitor production performance

EOF
    
    log_success "Comparison report generated: $report_file"
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
    log_section "GoImpCore Performance Comparison Test"
    log_info "Testing bottleneck analysis optimizations vs baseline"
    
    # Set trap for cleanup
    trap cleanup EXIT INT TERM
    
    # Setup
    setup_test
    
    # Test configurations
    local thread_counts=(4 8)
    
    # Run baseline tests
    for threads in "${thread_counts[@]}"; do
        run_performance_test "baseline" "$threads"
        sleep 5 # Cool-down period
    done
    
    # Run optimized tests
    for threads in "${thread_counts[@]}"; do
        run_performance_test "optimized" "$threads"
        sleep 5 # Cool-down period
    done
    
    # Generate comparison report
    generate_comparison_report
    
    log_section "Performance Comparison Complete"
    log_success "All results saved to: $RESULTS_DIR"
    log_info "Review the comparison report for optimization effectiveness"
    
    # Show directory contents
    echo ""
    echo "Generated files:"
    ls -la "$RESULTS_DIR/"
}

# Check dependencies
check_dependencies() {
    local deps=("curl" "go" "bc" "jq")
    for dep in "${deps[@]}"; do
        if ! command -v "$dep" >/dev/null 2>&1; then
            log_warning "Recommended dependency not found: $dep (some features may be limited)"
        fi
    done
}

# Entry point
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    check_dependencies
    main "$@"
fi