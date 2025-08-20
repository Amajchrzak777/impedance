#!/bin/bash

# Manual test script to verify concurrent processing with real data

echo "=== Manual Concurrent Processing Test ==="
echo

# Kill any existing servers
echo "🔄 Cleaning up existing servers..."
pkill -f goimpsolver 2>/dev/null || true
lsof -ti :8080 | xargs kill -9 2>/dev/null || true
sleep 2

cd /Users/adammajchrzak/ghq/github.com/adam/masterapp/goimpcore/cmd/goimpsolver

# Function to create test data from all 12 CSV files
create_test_batch() {
    echo "📁 Creating test batch with all 12 impedance files..."
    
    # Generate JSON with all 12 CSV files
    echo '{' > test_batch.json
    echo '    "batch_id": "manual_test_batch_12",' >> test_batch.json
    echo '    "timestamp": "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",' >> test_batch.json
    echo '    "spectra": [' >> test_batch.json
    
    local impedance_dir="impedance_data"
    
    for i in {1..12}; do
        local file_num=$(printf "%03d" $i)
        local csv_file="$impedance_dir/impedance_data_$file_num.csv"
        
        if [ ! -f "$csv_file" ]; then
            echo "❌ Warning: $csv_file not found, skipping..."
            continue
        fi
        
        echo "🔄 Loading impedance_data_$file_num.csv..."
        
        # Read CSV data
        local frequencies=()
        local real_parts=()
        local imag_parts=()
        
        while IFS=',' read -r freq real imag; do
            # Convert scientific notation to decimal and remove whitespace
            freq=$(echo "$freq" | awk '{printf "%.6f", $1}' 2>/dev/null || echo "$freq" | xargs)
            real=$(echo "$real" | awk '{printf "%.6f", $1}' 2>/dev/null || echo "$real" | xargs)
            imag=$(echo "$imag" | awk '{printf "%.6f", $1}' 2>/dev/null || echo "$imag" | xargs)
            
            if [[ -n "$freq" && "$freq" != \#* ]]; then
                frequencies+=("$freq")
                real_parts+=("$real") 
                imag_parts+=("$imag")
            fi
        done < "$csv_file"
        
        echo "✅ Loaded ${#frequencies[@]} points from impedance_data_$file_num.csv"
        
        # Add spectrum to JSON
        echo '        {' >> test_batch.json
        echo '            "iteration": '$((i-1))',' >> test_batch.json
        echo '            "impedance_data": {' >> test_batch.json
        echo '                "timestamp": "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'",' >> test_batch.json
        
        # Add frequencies array
        echo -n '                "frequencies": [' >> test_batch.json
        for ((j=0; j<${#frequencies[@]}; j++)); do
            if [ $j -gt 0 ]; then echo -n ', ' >> test_batch.json; fi
            echo -n "${frequencies[j]}" >> test_batch.json
        done
        echo '],' >> test_batch.json
        
        # Add dummy magnitude and phase arrays (calculated from impedance)
        echo -n '                "magnitude": [' >> test_batch.json
        for ((j=0; j<${#real_parts[@]}; j++)); do
            if [ $j -gt 0 ]; then echo -n ', ' >> test_batch.json; fi
            local mag=$(echo "sqrt(${real_parts[j]}*${real_parts[j]} + ${imag_parts[j]}*${imag_parts[j]})" | bc -l 2>/dev/null || echo "1")
            printf "%.4f" "$mag" >> test_batch.json
        done
        echo '],' >> test_batch.json
        
        echo -n '                "phase": [' >> test_batch.json  
        for ((j=0; j<${#real_parts[@]}; j++)); do
            if [ $j -gt 0 ]; then echo -n ', ' >> test_batch.json; fi
            local phase=$(echo "atan2(${imag_parts[j]}, ${real_parts[j]}) * 180 / 3.14159" | bc -l 2>/dev/null || echo "0")
            printf "%.4f" "$phase" >> test_batch.json
        done
        echo '],' >> test_batch.json
        
        # Add impedance array
        echo '                "impedance": [' >> test_batch.json
        for ((j=0; j<${#real_parts[@]}; j++)); do
            if [ $j -gt 0 ]; then echo ',' >> test_batch.json; fi
            printf '                    {"real": %.6f, "imag": %.6f}' "${real_parts[j]}" "${imag_parts[j]}" >> test_batch.json
        done
        echo '' >> test_batch.json
        echo '                ]' >> test_batch.json
        echo '            }' >> test_batch.json
        
        if [ $i -lt 12 ]; then
            echo '        },' >> test_batch.json
        else
            echo '        }' >> test_batch.json
        fi
    done
    
    echo '    ]' >> test_batch.json
    echo '}' >> test_batch.json
    
    echo "✅ Test batch created with 12 spectra from real CSV files"
}

# Test different concurrency levels
test_concurrency_manual() {
    local threads=$1
    
    echo "🧵 Testing with $threads goroutines (12 spectra)..."
    
    # Start server
    echo "📡 Starting server..."
    ./goimpsolver -http -threads=$threads -q &
    SERVER_PID=$!
    
    # Wait for server to start
    sleep 3
    
    # Check if server is running
    if ! curl -s http://localhost:8080/eis-data >/dev/null 2>&1; then
        echo "❌ Server failed to start"
        kill $SERVER_PID 2>/dev/null
        return 1
    fi
    
    echo "✅ Server started (PID: $SERVER_PID)"
    
    # Send request
    echo "📤 Sending batch request..."
    local start_time=$(date +%s.%N)
    
    local response=$(curl -s -X POST \
        -H "Content-Type: application/json" \
        -d @test_batch.json \
        http://localhost:8080/eis-data/batch)
    
    local end_time=$(date +%s.%N)
    local request_time=$(echo "$end_time - $start_time" | bc -l)
    
    # Check response
    if echo "$response" | grep -q '"success":true'; then
        echo "✅ Request successful in ${request_time}s"
        
        # Wait for processing with 15 second timeout
        echo "⏳ Waiting 15 seconds for 12 EIS optimizations to complete..."
        echo "📊 Processing status:"
        for i in {1..15}; do
            echo -n "."
            sleep 1
        done
        echo ""
        
        echo "📊 Processing completed"
    else
        echo "❌ Request failed:"
        echo "$response"
    fi
    
    # Stop server
    echo "🛑 Stopping server..."
    kill $SERVER_PID 2>/dev/null
    sleep 2
    
    echo
}

# Main execution
main() {
    echo "Building server..."
    go build -o goimpsolver
    
    if [ ! -f "./goimpsolver" ]; then
        echo "❌ Failed to build server"
        exit 1
    fi
    
    echo "✅ Server built"
    echo
    
    # Create test data
    create_test_batch
    echo
    
    # Test different concurrency levels
    test_concurrency_manual 1
    test_concurrency_manual 5
    test_concurrency_manual 10
    test_concurrency_manual 12
    
    echo "=== Manual Test Complete ==="
    
    # Show results
    if [ -f "concurrent_timing_results.csv" ]; then
        echo "📊 Latest results:"
        tail -n 4 concurrent_timing_results.csv | column -t -s ','
    fi
    
    # Cleanup
    rm -f test_batch.json
}

main "$@"