# GoImpCore - Restructured Architecture

This document describes the restructured, well-organized architecture of the GoImpCore project with clear separation of concerns and improved maintainability.

## 📁 Project Structure

```
goimpcore/
├── pkg/                          # Public packages (reusable components)
│   ├── config/                   # Configuration management
│   │   └── config.go            # Config structs and defaults
│   ├── models/                   # Data models and structures
│   │   └── models.go            # All model definitions
│   ├── worker/                   # Worker pool management
│   │   └── pool.go              # Concurrent worker pool implementation
│   ├── webhook/                  # Webhook processing
│   │   ├── client.go            # HTTP webhook client
│   │   └── impedance.go         # Impedance calculations
│   ├── handlers/                 # HTTP request handlers
│   │   ├── eis.go               # Single EIS data handler
│   │   └── batch.go             # Batch EIS data handler
│   └── server/                   # HTTP server setup
│       └── server.go            # Server initialization and routing
├── internal/                     # Private packages (internal use only)
│   ├── processing/               # EIS data processing logic
│   │   └── eis.go               # Core EIS processing
│   └── utils/                    # Utility functions
│       └── id.go                # ID generation utilities
└── cmd/
    └── goimpsolver-restructured/ # New main application
        └── main.go              # Clean application entry point
```

## 🏗️ Architecture Overview

### Key Principles

1. **Separation of Concerns**: Each package has a single, well-defined responsibility
2. **Clean Interfaces**: Components communicate through well-defined interfaces
3. **Dependency Injection**: Dependencies are injected rather than hard-coded
4. **Async Processing**: Heavy computation is handled asynchronously
5. **Error Handling**: Comprehensive error handling throughout the stack

### Component Responsibilities

#### `/pkg/config` - Configuration Management
- Centralized configuration structures
- Default configuration values
- Command-line flag handling support

#### `/pkg/models` - Data Models
- All data structures used across the application
- Clear typing for request/response payloads
- Shared models for inter-component communication

#### `/pkg/worker` - Worker Pool Management
- **Why webhookQueue is multiplied by 4**: The webhook queue buffer is 4x larger than job/result queues because:
  - Webhooks are processed asynchronously and can experience network latency
  - HTTP requests may fail and require retries
  - Decouples EIS processing speed from webhook delivery speed
  - Prevents webhook bottlenecks from blocking core processing

- **Key Features**:
  - Configurable worker count
  - Buffer pool for memory reuse
  - Non-blocking webhook queuing
  - Graceful shutdown support

#### `/pkg/webhook` - Webhook Processing
- HTTP client for webhook delivery
- Impedance calculation for circuit elements
- JSON sanitization for invalid float values
- Error handling and retry logic

#### `/pkg/handlers` - HTTP Request Handlers
- Clean separation of single vs batch processing
- CORS handling
- Request validation and error responses
- Async processing coordination

#### `/pkg/server` - Server Management
- HTTP server setup and configuration
- Route registration
- Health check endpoints
- Graceful shutdown handling

#### `/internal/processing` - EIS Processing
- Core EIS data processing logic
- Integration with goimpcore library
- Error handling and validation
- Result formatting

#### `/internal/utils` - Utilities
- ID generation
- Common utility functions
- Internal helper methods

## 🚀 Usage

### Running the Restructured Server

```bash
cd cmd/goimpsolver-restructured
go run main.go -server -threads=8 -code="R(RC)"
```

### Configuration Options

- `-code`: Circuit code (default: "R(RC)")
- `-threads`: Number of worker threads (default: 5)
- `-quiet`: Suppress verbose output
- `-server`: Start HTTP server
- `-benchmark`: Enable benchmark mode

### API Endpoints

- `POST /eis-data` - Process single EIS measurement
- `POST /eis-data/batch` - Process batch of EIS measurements
- `GET /health` - Health check endpoint

## 🔄 Async Operations Flow

### Single EIS Processing
1. HTTP request received → Handler validates input
2. Job queued to worker pool → Worker processes EIS data
3. Result returned → Webhook queued asynchronously
4. Immediate response sent to client
5. Webhook processed in background

### Batch EIS Processing
1. Batch request received → Handler validates batch
2. Multiple jobs queued to worker pool
3. Workers process jobs concurrently
4. Results collected and timing recorded
5. Webhooks queued for all results
6. Metrics saved to file

## 🛠️ Key Improvements

### Original Issues Addressed
- **Monolithic server.go**: Split into focused, single-responsibility packages
- **Mixed concerns**: Clear separation between HTTP handling, processing, and webhook delivery
- **Hard-coded dependencies**: Dependency injection pattern implemented
- **Poor error handling**: Comprehensive error handling added throughout
- **Difficult testing**: Clean interfaces enable easy unit testing

### Performance Optimizations
- **Buffer pooling**: Reuse buffers to reduce garbage collection
- **Async webhooks**: Webhook delivery doesn't block processing
- **Configurable workers**: Adjust concurrency based on workload
- **Non-blocking queues**: Prevents one slow component from blocking others

### Maintainability Features
- **Clear package boundaries**: Easy to understand and modify
- **Dependency injection**: Easy to swap implementations for testing
- **Comprehensive logging**: Better observability and debugging
- **Graceful shutdown**: Clean resource cleanup on termination

## 🧪 Testing Strategy

The restructured code enables easy unit testing:

```go
// Example: Testing webhook client
func TestWebhookClient_Send(t *testing.T) {
    // Create test server
    server := httptest.NewServer(...)
    
    // Create webhook client
    client := webhook.NewClient(server.URL, config)
    
    // Test webhook sending
    err := client.Send(testWebhook)
    assert.NoError(t, err)
}
```

## 📈 Migration Path

1. **Phase 1**: Run new restructured code alongside existing code
2. **Phase 2**: Integrate actual EIS processing from original code
3. **Phase 3**: Add comprehensive tests
4. **Phase 4**: Replace original server.go with restructured version

This restructured architecture provides a solid foundation for maintainable, scalable EIS processing with clean separation of asynchronous operations.