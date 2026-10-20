// Package websocket provides a bounded, process-local WebSocket hub with a
// versioned envelope, topic authorization, explicit backpressure classes, and
// lifecycle-aware draining.
//
// The package owns exactly one reader and one writer pump per connection and
// keeps its transport implementation private. Delivery is non-durable: clients
// must resynchronize authoritative state through an application-owned durable
// API after reconnecting. A caller should run each Hub once under its process
// lifecycle and invoke Shutdown with a bounded drain deadline.
package websocket
