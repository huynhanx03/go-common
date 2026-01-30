# Shared Library

This directory contains production-ready, reusable components for building Go microservices.

| Package | Sub-package | Description |
|---------|-------------|-------------|
| **common** | | Core framework primitives |
| | apperr | Unified application error codes and messages |
| | cache | Caching strategies and interfaces |
| | http | HTTP request parsing, response formatting, handler wrappers |
| | locks | Distributed locking mechanisms |
| | workerpool | Concurrent worker pool implementation |
| **database** | | Data layer adapters |
| | ent | MySQL adapter using Ent ORM |
| | mongodb | MongoDB adapter |
| | elasticsearch | Elasticsearch adapter |
| | redis | Redis adapter |
| | widecolumn | Wide-column store adapter |
| **mq** | | Message queue adapters |
| | kafka | Kafka producer/consumer implementation |
| | batcher | Message batching utilities |
| **datastructs** | | High-performance data structures |
| | bloom | Bloom filter for probabilistic membership testing |
| | btree | B-tree implementation |
| | buffer | Ring buffer and buffer utilities |
| | queue | Queue implementations |
| | shardedmap | Sharded concurrent map for high-throughput scenarios |
| | sketch | Count-min sketch for frequency estimation |
| **cdc** | | Change Data Capture utilities for data synchronization |
| **dto** | | Data Transfer Objects and pagination contracts |
| **algorithm** | | Common algorithms |
| **constraints** | | Generic type constraints for Go generics |
| **encoding** | | Encoding/decoding utilities |
| **hash** | | Hashing utilities |
| **logger** | | Structured logging |
| **pool** | | Object pooling for memory efficiency |
| **runtime** | | Runtime utilities (goroutine management) |
| **security** | | Security utilities |
| **settings** | | Configuration management |
| **timer** | | Timer and scheduling utilities |
| **unique** | | Unique ID generation |
| **utils** | | General-purpose helper functions |
