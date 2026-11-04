package json

import (
	"bytes"
	stdjson "encoding/json"
	jsontext "encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"testing"

	"github.com/bytedance/sonic"
)

var benchmarkDocument = []byte(`{"name":"checkout-v2","enabled":true,"rules":[{"id":"beta","percent":20},{"id":"staff","percent":100}],"labels":{"region":"apac","tier":2,"nested":{"active":true}}}`)

type benchmarkFlagDocument struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Rules   []struct {
		ID      string `json:"id"`
		Percent int    `json:"percent"`
	} `json:"rules"`
	Labels map[string]any `json:"labels"`
}

func BenchmarkUnmarshalEngines(b *testing.B) {
	benchmarks := []struct {
		name      string
		unmarshal func(*benchmarkFlagDocument) error
	}{
		{
			name: "Sonic",
			unmarshal: func(target *benchmarkFlagDocument) error {
				return sonic.Unmarshal(benchmarkDocument, target)
			},
		},
		{
			name: "EncodingJSONv1",
			unmarshal: func(target *benchmarkFlagDocument) error {
				return stdjson.Unmarshal(benchmarkDocument, target)
			},
		},
		{
			name: "EncodingJSONv2Strict",
			unmarshal: func(target *benchmarkFlagDocument) error {
				return jsonv2.Unmarshal(
					benchmarkDocument,
					target,
					jsonv2.RejectUnknownMembers(true),
				)
			},
		},
		{
			name: "UnmarshalStrict",
			unmarshal: func(target *benchmarkFlagDocument) error {
				return UnmarshalStrict(benchmarkDocument, target)
			},
		},
		{
			name: "JSONTextScanThenSonic",
			unmarshal: func(target *benchmarkFlagDocument) error {
				return unmarshalWithJSONTextScan(benchmarkDocument, target)
			},
		},
	}

	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(benchmarkDocument)))
			b.ResetTimer()
			for range b.N {
				var target benchmarkFlagDocument
				if err := benchmark.unmarshal(&target); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// unmarshalWithJSONTextScan benchmarks the previous strict strategy: scan the
// full document for syntax, duplicate names, and nesting, then decode with
// Sonic. The benchmark input is a valid, single JSON document.
func unmarshalWithJSONTextScan(data []byte, target any) error {
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	rootStarted := false
	rootComplete := false

	for {
		token, err := decoder.ReadToken()
		if errors.Is(err, io.EOF) {
			if !rootStarted || !rootComplete || decoder.StackDepth() != 0 {
				return ErrInvalidDocument
			}
			break
		}
		if err != nil {
			return err
		}
		if rootComplete {
			return ErrInvalidDocument
		}
		rootStarted = true

		switch token.Kind() {
		case jsontext.KindBeginObject, jsontext.KindBeginArray:
			if decoder.StackDepth() > maxStrictNesting {
				return ErrInvalidDocument
			}
		case jsontext.KindEndObject, jsontext.KindEndArray:
			if decoder.StackDepth() == 0 {
				rootComplete = true
			}
		default:
			if decoder.StackDepth() == 0 {
				rootComplete = true
			}
		}
	}

	sonicDecoder := sonic.ConfigDefault.NewDecoder(bytes.NewReader(data))
	sonicDecoder.DisallowUnknownFields()
	if err := sonicDecoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := sonicDecoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrInvalidDocument
	}
	return nil
}
