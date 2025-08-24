#!/bin/bash

# Optimized GoImpCore Startup Script
# Based on bottleneck analysis recommendations

set -e

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[OPTIMIZATION]${NC} $1"
}

# Default values
THREADS=4
PROFILE=false
MEMORY_OPTIMIZED=false

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -t|--threads)
            THREADS="$2"
            shift 2
            ;;
        -p|--profile)
            PROFILE=true
            shift
            ;;
        -m|--memory-optimized)
            MEMORY_OPTIMIZED=true
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [OPTIONS]"
            echo "Options:"
            echo "  -t, --threads N        Number of worker threads (default: 4)"
            echo "  -p, --profile         Enable profiling"
            echo "  -m, --memory-optimized Enable memory optimizations"
            echo "  -h, --help            Show this help"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

# Build if needed
if [ ! -f "./goimpsolver-restructured" ] || [ "main.go" -nt "./goimpsolver-restructured" ]; then
    log_info "Building server..."
    go build
    log_success "Server built successfully"
fi

# Set up optimized environment variables
log_warning "Applying performance optimizations..."

# GC Optimization based on analysis
if [ "$MEMORY_OPTIMIZED" = true ]; then
    # Low latency GC settings for high-throughput scenarios
    export GOGC=50
    log_warning "GC tuned for low latency (GOGC=50)"
    
    # Enable more detailed GC tracing if profiling is enabled
    if [ "$PROFILE" = true ]; then
        export GODEBUG=gctrace=1
        log_warning "GC tracing enabled for detailed analysis"
    fi
else
    # Balanced GC settings (default 100)
    export GOGC=80
    log_warning "GC tuned for balanced performance (GOGC=80)"
fi

# Memory allocation optimization
export GOMEMLIMIT=2GiB  # Set reasonable memory limit
log_warning "Memory limit set to 2GB"

# Network optimization for webhook performance
export GOMAXPROCS=0  # Use all available CPUs
log_warning "Using all available CPU cores"

# TCP optimization for better connection handling
if [ "$PROFILE" = true ]; then
    export GODEBUG="${GODEBUG},http2debug=1"
    log_warning "HTTP/2 debugging enabled for profiling"
fi

# Log current settings
echo ""
log_info "Starting GoImpCore with optimized settings:"
echo "  - Worker threads: $THREADS"
echo "  - Profiling: $PROFILE" 
echo "  - Memory optimized: $MEMORY_OPTIMIZED"
echo "  - GOGC: $GOGC"
echo "  - GOMEMLIMIT: $GOMEMLIMIT"
echo ""

# Build command line arguments
CMD_ARGS="-server -threads=$THREADS"
if [ "$PROFILE" = true ]; then
    CMD_ARGS="$CMD_ARGS -profile"
fi

log_info "Executing: ./goimpsolver-restructured $CMD_ARGS"

# Start the server
exec ./goimpsolver-restructured $CMD_ARGS