#!/bin/bash

# Test script for concurrent spectrum processing benchmark
# Tests 1, 5, and 10 goroutines with sample EIS data

echo "=== Concurrent Spectrum Processing Benchmark ==="
echo "Testing different concurrency levels..."
echo

# Function to load impedance data from CSV file
load_impedance_csv() {
    local csv_file="$1"
    
    if [ ! -f "$csv_file" ]; then
        echo "❌ Error: File not found: $csv_file" >&2
        return 1
    fi
    
    # Read CSV data into arrays
    frequencies=()
    real_parts=()
    imag_parts=()
    magnitudes=()
    phases=()
    
    while IFS=',' read -r freq real imag; do
        # Skip empty lines and remove any whitespace
        freq=$(echo "$freq" | xargs)
        real=$(echo "$real" | xargs)
        imag=$(echo "$imag" | xargs)
        
        if [[ -n "$freq" && "$freq" != \#* && "$freq" != "frequency"* ]]; then
            frequencies+=("$freq")
            real_parts+=("$real")  
            imag_parts+=("$imag")
            
            # Calculate magnitude and phase
            local mag=$(echo "sqrt($real*$real + $imag*$imag)" | bc -l 2>/dev/null || echo "0")
            local phase=$(echo "if ($real == 0) 0 else atan2($imag, $real) * 180 / 3.14159" | bc -l 2>/dev/null || echo "0")
            magnitudes+=("$mag")
            phases+=("$phase")
        fi
    done < "$csv_file"
    
    echo "${#frequencies[@]}"  # Return number of data points loaded
}

# Function to generate test data using all 12 impedance CSV files
generate_test_data_from_csvs() {
    local impedance_dir="/Users/adammajchrzak/ghq/github.com/adam/masterapp/goimpcore/cmd/goimpsolver/impedance_data"
    local num_files=${1:-12}  # Default to all 12 files
    
    echo "📁 Loading impedance data from CSV files..."
    
    cat << EOF
{
    "batch_id": "csv_batch_$(date +%s)",
    "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
    "spectra": [
EOF
    
    for ((i=1; i<=num_files && i<=12; i++)); do
        local file_num=$(printf "%03d" $i)
        local csv_file="$impedance_dir/impedance_data_$file_num.csv"
        
        echo "🔄 Processing file $i/12: impedance_data_$file_num.csv" >&2
        
        # Load data from CSV
        local point_count=$(load_impedance_csv "$csv_file")
        
        if [ "$point_count" -eq 0 ]; then
            echo "❌ No data points loaded from $csv_file" >&2
            continue
        fi
        
        echo "✅ Loaded $point_count points from impedance_data_$file_num.csv" >&2
        
        echo "        {"
        echo "            \"iteration\": $((i-1)),"
        echo "            \"impedance_data\": {"
        echo "                \"timestamp\": \"$(date -u +%Y-%m-%dT%H:%M:%SZ)\","
        
        # Output frequencies array
        echo -n "                \"frequencies\": ["
        for ((j=0; j<${#frequencies[@]}; j++)); do
            if [ $j -gt 0 ]; then echo -n ", "; fi
            printf "%.6g" "${frequencies[j]}"
        done
        echo "],"
        
        # Output magnitudes array  
        echo -n "                \"magnitude\": ["
        for ((j=0; j<${#magnitudes[@]}; j++)); do
            if [ $j -gt 0 ]; then echo -n ", "; fi
            printf "%.6f" "${magnitudes[j]}"
        done
        echo "],"
        
        # Output phases array
        echo -n "                \"phase\": ["
        for ((j=0; j<${#phases[@]}; j++)); do
            if [ $j -gt 0 ]; then echo -n ", "; fi
            printf "%.6f" "${phases[j]}"
        done
        echo "],"
        
        # Output impedance array (real + imaginary parts)
        echo "                \"impedance\": ["
        for ((j=0; j<${#real_parts[@]}; j++)); do
            if [ $j -gt 0 ]; then echo ","; fi
            printf "                    {\"real\": %.6f, \"imag\": %.6f}" "${real_parts[j]}" "${imag_parts[j]}"
        done
        echo ""
        echo "                ]"
        echo "            }"
        if [ $i -lt $num_files ]; then
            echo "        },"
        else
            echo "        }"
        fi
    done
    
    echo "    ]"
    echo "}"
}

# Wrapper function for backward compatibility
generate_test_data() {
    local num_spectra=${1:-12}
    generate_test_data_from_csvs $num_spectra
}

# Start the server in background if not already running
start_server() {
    local threads=$1
    echo "Starting server with $threads threads..."
    
    # Kill any existing server
    pkill -f goimpsolver || true
    sleep 1
    
    # Start new server
    cd /Users/adammajchrzak/ghq/github.com/adam/masterapp/goimpcore/cmd/goimpsolver
    ./goimpsolver -http -threads=$threads -q &
    SERVER_PID=$!
    
    # Wait for server to start
    sleep 2
    
    # Check if server is running
    if ! curl -s http://localhost:8080/eis-data >/dev/null 2>&1; then
        echo "Failed to start server!"
        return 1
    fi
    
    echo "Server started with PID $SERVER_PID"
    return 0
}

# Test function
test_concurrency() {
    local threads=$1
    local num_spectra=${2:-10}
    
    echo "🧵 Testing with $threads goroutines, $num_spectra spectra..."
    
    # Start server with specific thread count
    if ! start_server $threads; then
        echo "❌ Failed to start server"
        return 1
    fi
    
    # Generate test data
    local test_data=$(generate_test_data $num_spectra)
    
    # Send request and measure time
    local start_time=$(date +%s.%N)
    
    local response=$(curl -s -X POST \
        -H "Content-Type: application/json" \
        -d "$test_data" \
        http://localhost:8080/eis-data/batch)
    
    local end_time=$(date +%s.%N)
    local request_time=$(echo "$end_time - $start_time" | bc -l)
    
    # Check response
    if echo "$response" | grep -q '"success":true'; then
        echo "✅ Request sent successfully in ${request_time}s"
        
        # Wait for processing to complete (real EIS optimization takes longer)
        # Estimate: ~5-10 seconds per spectrum for EIS mode with 5 iterations
        local wait_time=$((num_spectra * 8 / threads + 10))
        echo "⏳ Waiting ${wait_time}s for $num_spectra real EIS optimizations to complete..."
        sleep $wait_time
        
        echo "📊 Processing completed"
    else
        echo "❌ Request failed: $response"
    fi
    
    # Stop server
    kill $SERVER_PID 2>/dev/null || true
    sleep 1
    
    echo
}

# Main test execution
main() {
    local workers=${1:-"1,5,10"}  # Default workers if no parameter provided
    
    echo "Building server..."
    cd /Users/adammajchrzak/ghq/github.com/adam/masterapp/goimpcore/cmd/goimpsolver
    go build
    
    if [ ! -f "./goimpsolver" ]; then
        echo "❌ Failed to build server"
        exit 1
    fi
    
    echo "✅ Server built successfully"
    echo
    
    # Test different concurrency levels using all 12 CSV files
    local num_spectra=12  # All 12 impedance CSV files
    
    echo "Testing with $num_spectra real EIS spectra from CSV files"
    echo "========================================================"
    echo "Files: impedance_data_001.csv through impedance_data_012.csv"
    echo "Workers: $workers"
    echo
    
    # Parse workers parameter (comma-separated list)
    IFS=',' read -ra WORKER_ARRAY <<< "$workers"
    for worker_count in "${WORKER_ARRAY[@]}"; do
        test_concurrency "$worker_count" $num_spectra
    done
    
    echo "=== Benchmark Complete ==="
    echo
    echo "📁 Results saved to: concurrent_timing_results.csv"
    
    # Show results if file exists
    if [ -f "concurrent_timing_results.csv" ]; then
        echo "📊 Latest results:"
        tail -n ${#WORKER_ARRAY[@]} concurrent_timing_results.csv | column -t -s ','
    fi
}

# Run main function
main "$@"