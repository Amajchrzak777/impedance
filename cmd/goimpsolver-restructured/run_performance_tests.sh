#!/bin/bash

# Performance test script for EIS analysis
# Tests different thread counts and optimization methods

echo "=== EIS Performance Test ==="
echo "📊 Testing:"
echo "   - Thread counts: 1, 5, 10, 12"  
echo "   - Methods: nelder-mead, levenberg-marquardt"
echo "   - Circuit: R(QR)"
echo

# Kill any existing servers
echo "🔄 Cleaning up existing servers..."
pkill -f goimpsolver 2>/dev/null || true
lsof -ti :8080 | xargs kill -9 2>/dev/null || true
sleep 2

# Create test data if it doesn't exist
create_test_data() {
    if [ ! -f "test_batch.json" ]; then
        echo "📁 Creating test batch..."
        cat > test_batch.json << 'EOF'
{
    "batch_id": "performance_test",
    "timestamp": "2025-01-01T12:00:00Z",
    "spectra": [
        {
            "iteration": 0,
            "impedance_data": {
                "timestamp": "2025-01-01T12:00:00Z",
                "frequencies": [1000.0, 750.0, 500.0, 250.0, 100.0, 50.0, 25.0, 10.0, 5.0, 1.0],
                "magnitude": [100.0, 95.0, 90.0, 85.0, 80.0, 75.0, 70.0, 65.0, 60.0, 55.0],
                "phase": [-10.0, -15.0, -20.0, -25.0, -30.0, -35.0, -40.0, -45.0, -50.0, -55.0],
                "impedance": [
                    {"real": 98.48, "imag": -17.36},
                    {"real": 91.92, "imag": -24.52},
                    {"real": 84.64, "imag": -30.64},
                    {"real": 76.92, "imag": -35.83},
                    {"real": 69.28, "imag": -40.00},
                    {"real": 61.44, "imag": -43.30},
                    {"real": 53.46, "imag": -44.90},
                    {"real": 45.96, "imag": -45.24},
                    {"real": 38.57, "imag": -45.96},
                    {"real": 31.18, "imag": -45.69}
                ]
            }
        },
        {
            "iteration": 1,
            "impedance_data": {
                "timestamp": "2025-01-01T12:00:00Z",
                "frequencies": [1000.0, 750.0, 500.0, 250.0, 100.0, 50.0, 25.0, 10.0, 5.0, 1.0],
                "magnitude": [101.0, 96.0, 91.0, 86.0, 81.0, 76.0, 71.0, 66.0, 61.0, 56.0],
                "phase": [-9.0, -14.0, -19.0, -24.0, -29.0, -34.0, -39.0, -44.0, -49.0, -54.0],
                "impedance": [
                    {"real": 99.48, "imag": -16.36},
                    {"real": 92.92, "imag": -23.52},
                    {"real": 85.64, "imag": -29.64},
                    {"real": 77.92, "imag": -34.83},
                    {"real": 70.28, "imag": -39.00},
                    {"real": 62.44, "imag": -42.30},
                    {"real": 54.46, "imag": -43.90},
                    {"real": 46.96, "imag": -44.24},
                    {"real": 39.57, "imag": -44.96},
                    {"real": 32.18, "imag": -44.69}
                ]
            }
        }
    ]
}
EOF
        echo "✅ Test data created"
    fi
}

# Function to run test with specific configuration
run_test() {
    local threads=$1
    local method=$2
    
    echo "🧪 Testing: $threads threads, $method"
    echo "--------------------------------"
    
    # Start server
    echo "📡 Starting server..."
    ./goimpsolver-restructured -server -threads=$threads -quiet -method="$method" &
    SERVER_PID=$!
    
    # Wait for server to start
    sleep 3
    
    # Check if server is running
    if ! curl -s http://localhost:8080/eis-data >/dev/null 2>&1; then
        echo "❌ Server failed to start"
        kill $SERVER_PID 2>/dev/null || true
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
        
        # Wait for processing 
        local wait_time=10
        if [[ "$method" == "levenberg-marquardt" ]]; then
            wait_time=15  # LM is slower
        fi
        
        echo "⏳ Waiting ${wait_time} seconds for processing..."
        for ((i=1; i<=wait_time; i++)); do
            echo -n "."
            sleep 1
        done
        echo ""
        
        echo "✅ Processing completed"
    else
        echo "❌ Request failed:"
        echo "$response"
    fi
    
    # Stop server
    echo "🛑 Stopping server..."
    kill $SERVER_PID 2>/dev/null || true
    sleep 2
    echo
}

# Main execution
main() {
    echo "Building server..."
    go build -o goimpsolver-restructured
    
    if [ ! -f "./goimpsolver-restructured" ]; then
        echo "❌ Failed to build server"
        exit 1
    fi
    
    echo "✅ Server built"
    
    # Create test data
    create_test_data
    echo
    
    # Test configurations
    THREAD_COUNTS=(1 5 10 12)
    METHODS=("nelder-mead" "levenberg-marquardt")
    
    local total_tests=$((${#THREAD_COUNTS[@]} * ${#METHODS[@]}))
    local current_test=0
    
    echo "🎯 Running $total_tests test configurations"
    echo
    
    # Run tests
    for threads in "${THREAD_COUNTS[@]}"; do
        for method in "${METHODS[@]}"; do
            current_test=$((current_test + 1))
            echo "🔄 Test $current_test/$total_tests"
            run_test "$threads" "$method"
            sleep 1
        done
    done
    
    echo "=== Performance Test Complete ==="
    
    # Show results
    if [ -f "concurrent_timing_results.csv" ]; then
        echo "📊 Latest Results:"
        tail -n 8 concurrent_timing_results.csv | column -t -s ','
    fi
    
    # Cleanup
    rm -f test_batch.json
    
    echo "💾 Results saved to concurrent_timing_results.csv"
}

main "$@"