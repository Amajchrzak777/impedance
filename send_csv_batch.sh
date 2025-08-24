#!/bin/bash

# Script to send CSV data as batch request to GoImpCore EIS server
#
# Usage: ./send_csv_batch.sh [csv_file] [batch_id] [server_port]

set -e

# Default values
CSV_FILE="${1:-cmd/goimpsolver/impedance_data/combined_impedance_data.csv}"
BATCH_ID="${2:-csv-batch-$(date +%s)}"
SERVER_PORT="${3:-8080}"
SERVER_URL="http://localhost:${SERVER_PORT}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${BLUE}🔄 GoImpCore CSV Batch Sender${NC}"
echo -e "CSV File: ${YELLOW}${CSV_FILE}${NC}"
echo -e "Batch ID: ${YELLOW}${BATCH_ID}${NC}"
echo -e "Server:   ${YELLOW}${SERVER_URL}${NC}"
echo ""

# Check if CSV file exists
if [[ ! -f "$CSV_FILE" ]]; then
    echo -e "${RED}❌ Error: CSV file not found: $CSV_FILE${NC}"
    exit 1
fi

# Check if server is running
if ! curl -s "${SERVER_URL}/health" > /dev/null; then
    echo -e "${RED}❌ Error: Server not responding at ${SERVER_URL}${NC}"
    echo -e "   Start server with: ${YELLOW}./goimpsolver-restructured -server -threads=4 -profile${NC}"
    exit 1
fi

echo -e "${GREEN}✅ Server is running${NC}"

# Convert CSV to JSON
TEMP_JSON=$(mktemp /tmp/batch_request_XXXXXX.json)
echo -e "${BLUE}📊 Converting CSV to batch JSON...${NC}"

if ! python3 csv_to_batch.py "$CSV_FILE" "$BATCH_ID" > "$TEMP_JSON" 2>/dev/null; then
    echo -e "${RED}❌ Error: Failed to convert CSV to JSON${NC}"
    rm -f "$TEMP_JSON"
    exit 1
fi

# Get JSON file size
JSON_SIZE=$(du -h "$TEMP_JSON" | cut -f1)
echo -e "${GREEN}✅ Conversion complete (${JSON_SIZE})${NC}"

# Send batch request
echo -e "${BLUE}🚀 Sending batch request...${NC}"

RESPONSE=$(curl -s -w "HTTPSTATUS:%{http_code};TIME:%{time_total}" \
    -X POST \
    -H "Content-Type: application/json" \
    -d @"$TEMP_JSON" \
    "${SERVER_URL}/eis-data/batch")

# Parse response
HTTP_BODY=$(echo "$RESPONSE" | sed -E 's/HTTPSTATUS:[0-9]{3};TIME:[0-9.]+$//')
HTTP_STATUS=$(echo "$RESPONSE" | grep -o "HTTPSTATUS:[0-9]*" | cut -d: -f2)
RESPONSE_TIME=$(echo "$RESPONSE" | grep -o "TIME:[0-9.]*" | cut -d: -f2)

# Clean up
rm -f "$TEMP_JSON"

# Display results
echo ""
if [[ "$HTTP_STATUS" == "202" ]]; then
    echo -e "${GREEN}✅ Batch request accepted!${NC}"
    echo -e "Response: ${GREEN}$HTTP_BODY${NC}"
    echo -e "Time: ${BLUE}${RESPONSE_TIME}s${NC}"
    echo ""
    echo -e "${YELLOW}📈 Monitoring endpoints:${NC}"
    echo -e "  Profiling: http://localhost:6060/debug/pprof/"
    echo -e "  Runtime:   http://localhost:6060/debug/info"
    echo -e "  GC Stats:  http://localhost:${SERVER_PORT}/debug/gc"
else
    echo -e "${RED}❌ Request failed (HTTP $HTTP_STATUS)${NC}"
    echo -e "Response: ${RED}$HTTP_BODY${NC}"
    exit 1
fi

# Optional: Show real-time profiling info
echo ""
read -p "Show real-time system info? (y/N): " -n 1 -r
echo ""
if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo -e "${BLUE}📊 Real-time system info:${NC}"
    for i in {1..5}; do
        echo -e "${YELLOW}[${i}/5]${NC} $(date '+%H:%M:%S')"
        curl -s http://localhost:6060/debug/info | \
            jq -r '"  Goroutines: \(.goroutines), Memory: \(.memory.alloc_mb)MB, GC: \(.gc.num_gc), Objects: \(.memory.heap_objects)"'
        [[ $i -lt 5 ]] && sleep 2
    done
fi

echo -e "${GREEN}✅ Done!${NC}"