package forge

import (
	"fmt"
	"testing"
)

// BenchmarkProducerThroughput measures single-producer write throughput.
func BenchmarkProducerThroughput(b *testing.B) {
	dir := b.TempDir()
	br, _ := NewBroker(dir)
	defer br.Close()

	p, _ := br.NewProducer("bench", WithBatchSize(64*1024))
	defer p.Close()

	value := make([]byte, 100) // 100-byte messages
	for i := range value {
		value[i] = byte(i)
	}

	b.SetBytes(100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := p.Send(nil, value, nil); err != nil {
			b.Fatal(err)
		}
		// Flush every 1000 to simulate real batching.
		if i%1000 == 999 {
			p.Flush()
		}
	}
	p.Flush()
}

// BenchmarkBatchEncodeDecode measures raw encode/decode performance.
func BenchmarkBatchEncodeDecode(b *testing.B) {
	batch := &RecordBatch{
		Compression: CompressionNone,
		Timestamp:   1000000,
		MaxTimestamp: 1000000,
		RecordCount: 10,
	}
	for i := 0; i < 10; i++ {
		batch.Records = append(batch.Records, Record{
			OffsetDelta: int64(i),
			Key:         []byte(fmt.Sprintf("key-%d", i)),
			Value:       make([]byte, 100),
		})
	}

	encoded, _ := EncodeBatch(batch, nil)
	b.SetBytes(int64(len(encoded)))

	b.Run("Encode", func(b *testing.B) {
		dst := make([]byte, len(encoded)*2)
		for i := 0; i < b.N; i++ {
			EncodeBatch(batch, dst)
		}
	})

	b.Run("Decode", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			DecodeBatch(encoded)
		}
	})
}

// BenchmarkCommitLogAppend measures raw append throughput to commit log.
func BenchmarkCommitLogAppend(b *testing.B) {
	dir := b.TempDir()
	cl, _ := NewCommitLog(dir)
	defer cl.Close()

	batch := &RecordBatch{
		Compression: CompressionNone,
		Timestamp:   1,
		MaxTimestamp: 1,
		Records: []Record{
			{Value: make([]byte, 100)},
			{Value: make([]byte, 100)},
			{Value: make([]byte, 100)},
			{Value: make([]byte, 100)},
			{Value: make([]byte, 100)},
		},
	}

	b.SetBytes(500)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := cl.Append(batch); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCommitLogRead measures sequential read throughput.
func BenchmarkCommitLogRead(b *testing.B) {
	dir := b.TempDir()
	cl, _ := NewCommitLog(dir)
	defer cl.Close()

	// Pre-fill with data.
	batch := &RecordBatch{
		Records: []Record{
			{Value: make([]byte, 100)},
			{Value: make([]byte, 100)},
		},
	}
	for i := 0; i < 10000; i++ {
		cl.Append(batch)
	}

	b.SetBytes(200)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		offset := uint64(i%9999) * 2
		cl.Read(offset, 4096)
	}
}

// BenchmarkLZ4ProducerThroughput measures write throughput with LZ4 compression.
func BenchmarkLZ4ProducerThroughput(b *testing.B) {
	dir := b.TempDir()
	br, _ := NewBroker(dir)
	defer br.Close()

	p, _ := br.NewProducer("lz4-bench", WithBatchSize(64*1024), WithCompression(CompressionLZ4))
	defer p.Close()

	value := make([]byte, 100)
	for i := range value {
		value[i] = byte(i % 26)
	}

	b.SetBytes(100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Send(nil, value, nil)
		if i%1000 == 999 {
			p.Flush()
		}
	}
	p.Flush()
}

// BenchmarkEndToEnd measures produce → flush → consume latency per message.
func BenchmarkEndToEnd(b *testing.B) {
	dir := b.TempDir()
	br, _ := NewBroker(dir)
	defer br.Close()

	p, _ := br.NewProducer("e2e", WithBatchSize(1<<20))
	c, _ := br.NewConsumer("bench-group", "e2e")

	value := make([]byte, 64)

	b.SetBytes(64)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.Send(nil, value, nil)
		if i%100 == 99 {
			p.Flush()
			c.Poll(100)
		}
	}
	p.Close()
}
