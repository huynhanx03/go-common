# Kafka Client Package

This package provides a robust, production-ready Kafka client wrapper for Go, built on top of `IBM/sarama`. It encapsulates complex configuration and provides high-level abstractions for producing and consuming messages with reliability and observability.

## Key Features

1.  **Middleware Pattern**: Supports middleware chaining for Consumers, allowing cross-cutting concerns (logging, metrics, recovery) to be applied to message processing.
2.  **Smart Retry Policy**: Implements exponential backoff strategies for connection retries, ensuring system stability during broker outages.
3.  **Context Propagation**: Automatically handles the injection and extraction of tracing context (Request ID, Trace ID) via Kafka message headers.
4.  **Flexible Producers**: specific implementations for both Synchronous (data safety) and Asynchronous (high throughput) publishing.
5.  **Graceful Shutdown**: Managed lifecycle for consumers to ensure in-flight messages are completed before termination.

## Configuration

The package uses a strongly-typed `Config` struct for initialization:

```go
cfg := &kafka.Config{
    Brokers: []string{"localhost:9092"},
    ConsumerInfo: kafka.ConsumerInfo{
        SessionTimeout: 10000, // ms
    },
}
```

## Usage

### Consumer

Consumers use a handler-based approach. Middleware can be applied globally to the consumer group.

**Example:**

```go
// Define a handler
func MessageHandler(ctx context.Context, msg *kafka.Message) error {
    // ctx contains propagated headers (e.g. TraceID)
    // Process message...
    return nil
}

// Initialize and Start
func main() {
    // Create Consumer with Recovery middleware
    consumer, _ := kafka.NewConsumer(cfg, "consumer-group-id", kafka.Recovery)

    // Start consuming in a blocking or non-blocking way
    go func() {
        err := consumer.Start(context.Background(), []string{"topic-name"}, MessageHandler, func(err error) {
            // Handle internal consumer errors
        })
    }()

    // Graceful shutdown on signal
    // ...
    consumer.Close()
}
```

### Producer

The package provides helpers for common patterns, such as publishing JSON data with context propagation.

**Example:**

```go
// Initialize Async Producer
producer, _ := kafka.NewAsyncProducer(cfg)

// Publish message
// This automatically injects TraceID/RequestID from ctx into Kafka Headers
err := kafka.PublishJSON(ctx, producer, "topic-name", func(model *MyModel) string {
    return model.ID // Partition Key
}, modelInstance)
```

## Architecture

### Middleware
Message processing follows an interceptor chain pattern:
`Middleware 1 -> Middleware 2 -> ... -> Handler`

### Context Propagation
-   **Producer**: Extracts `X-Request-ID` and `X-Trace-ID` from the Go `context.Context` and injects them as Kafka Record Headers.
-   **Consumer**: Reads Kafka Record Headers and reconstructs the `context.Context` before invoking the handler.
