# CSV to Batch Processing Guide

This guide shows how to send batch requests using CSV data files to the restructured GoImpCore server.

## 📂 CSV Data Format

The `combined_impedance_data.csv` file contains:
- **12 spectra** (spectrum numbers 1-12)  
- **23 frequency points** per spectrum
- **276 total data points**

```csv
Frequency_Hz,Z_real,Z_imag,Spectrum_Number
4.5007E+4,2.7606E+1,-1.6670E+1,1
2.8351E+4,2.6241E+1,-1.0561E+1,1
...
```

## 🚀 Quick Start

### Method 1: Use the Convenience Script (Recommended)

```bash
# Start the server with profiling
./goimpsolver-restructured -server -threads=4 -profile

# Send CSV data as batch request
./send_csv_batch.sh

# With custom parameters  
./send_csv_batch.sh path/to/data.csv "my-batch-id" 8080
```

### Method 2: Manual Conversion and Send

```bash
# 1. Convert CSV to JSON
python3 csv_to_batch.py cmd/goimpsolver/impedance_data/combined_impedance_data.csv "batch-001" > batch.json

# 2. Send batch request
curl -X POST -H "Content-Type: application/json" \
  -d @batch.json \
  http://localhost:8080/eis-data/batch
```

## 📊 Example Response

```json
{
  "batch_id": "csv-batch-1755808031",
  "message": "Batch processing started with worker pool", 
  "spectra": 12,
  "success": true
}
```

## 🔍 Monitoring During Processing

### Real-time System Info
```bash
curl -s http://localhost:6060/debug/info | jq
```

### Goroutine Status
```bash
curl -s "http://localhost:6060/debug/pprof/goroutine?debug=1" | head -10
```

### Memory Usage
```bash
curl -s http://localhost:6060/debug/info | jq '.memory'
```

### GC Statistics
```bash
curl -s http://localhost:8080/debug/gc | jq
```

## 📈 Performance Analysis

### During Large Batch Processing

You can monitor:
- **Worker Pool**: 4 worker goroutines processing spectra concurrently
- **Memory Usage**: Buffer pool efficiency and allocation patterns  
- **Webhook Queue**: Async webhook processing (4x buffer = 16 slots)
- **Processing Time**: Individual spectrum processing duration

### Sample Profiling Commands

```bash
# Collect CPU profile during processing
curl "http://localhost:6060/debug/pprof/profile?seconds=10" > batch_cpu.pprof

# Collect heap profile
curl "http://localhost:6060/debug/pprof/heap" > batch_heap.pprof

# Analyze with pprof
go tool pprof batch_cpu.pprof
go tool pprof batch_heap.pprof
```

## 🎯 Key Metrics from CSV Batch

Processing the full `combined_impedance_data.csv`:

- **Data Points**: 276 impedance measurements
- **Spectra**: 12 individual spectra  
- **Frequencies**: 23 points per spectrum (ranging from 3.0 Hz to 45,007 Hz)
- **Worker Concurrency**: 4 workers processing in parallel
- **Webhook Buffer**: 16-slot queue (4 workers × 4 buffer multiplier)

## 🔧 Customization

### Different CSV Formats

If your CSV has a different structure, modify `csv_to_batch.py`:

```python
# For different column names
reader = csv.DictReader(f)  
for row in reader:
    frequency = float(row['Freq'])      # Your frequency column
    z_real = float(row['Real_Z'])       # Your real impedance column  
    z_imag = float(row['Imag_Z'])       # Your imaginary impedance column
    spectrum_num = int(row['Series'])   # Your spectrum identifier
```

### Batch Processing Options

```bash
# Process only first 5 spectra
python3 csv_to_batch.py data.csv "partial-batch" | \
  jq '.spectra = .spectra[:5]' > partial_batch.json

# Custom batch ID with timestamp
BATCH_ID="experiment-$(date +%Y%m%d_%H%M%S)"
./send_csv_batch.sh data.csv "$BATCH_ID"
```

## 📝 Server Configuration

Optimize server performance for large batches:

```bash
# More workers for CPU-intensive processing
./goimpsolver-restructured -server -threads=8 -profile

# Environment tuning
export GOGC=50                    # More aggressive GC
export GOMAXPROCS=8               # Use more CPU cores
./goimpsolver-restructured -server -threads=8 -profile
```

This approach allows you to efficiently process large datasets from CSV files through the asynchronous EIS processing pipeline with full profiling capabilities.