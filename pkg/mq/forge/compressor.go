package forge

import (
	"errors"
	"fmt"

	"github.com/pierrec/lz4/v4"
)

// maxDecompressSize caps the LZ4 decompression buffer to prevent OOM from corrupt input.
const maxDecompressSize = 16 << 20 // 16 MB

// ErrIncompressible is returned when LZ4 cannot compress the data (random/encrypted payloads).
// Callers should fall back to CompressionNone.
var ErrIncompressible = errors.New("forge: data is incompressible")

// Compressor compresses and decompresses record batch payloads.
type Compressor interface {
	Compress(dst, src []byte) ([]byte, error)
	Decompress(dst, src []byte) ([]byte, error)
}

// noneCompressor is a passthrough (no compression).
type noneCompressor struct{}

func (noneCompressor) Compress(_, src []byte) ([]byte, error)   { return src, nil }
func (noneCompressor) Decompress(_, src []byte) ([]byte, error) { return src, nil }

// lz4Compressor uses LZ4 block compression.
type lz4Compressor struct{}

func (lz4Compressor) Compress(dst, src []byte) ([]byte, error) {
	bound := lz4.CompressBlockBound(len(src))
	if cap(dst) < bound {
		dst = make([]byte, bound)
	} else {
		dst = dst[:bound]
	}
	n, err := lz4.CompressBlock(src, dst, nil)
	if err != nil {
		return nil, err
	}
	if n == 0 {
		// Incompressible — signal caller to fall back to CompressionNone.
		return nil, ErrIncompressible
	}
	return dst[:n], nil
}

func (lz4Compressor) Decompress(dst, src []byte) ([]byte, error) {
	initSize := len(src) * decompressInitMultiplier
	if initSize > maxDecompressSize {
		initSize = maxDecompressSize
	}
	if cap(dst) < initSize {
		dst = make([]byte, initSize)
	} else {
		dst = dst[:cap(dst)]
	}
	for {
		n, err := lz4.UncompressBlock(src, dst)
		if err == nil {
			return dst[:n], nil
		}
		newSize := len(dst) * decompressGrowthFactor
		if newSize > maxDecompressSize {
			return nil, fmt.Errorf("forge: decompress exceeds %d bytes: %w", maxDecompressSize, err)
		}
		dst = make([]byte, newSize)
	}
}

// compressorFor returns the compressor for a given compression type.
func compressorFor(ct uint8) Compressor {
	if ct == CompressionLZ4 {
		return lz4Compressor{}
	}
	return noneCompressor{}
}
