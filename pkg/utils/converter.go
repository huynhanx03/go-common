package utils

import (
	"encoding/binary"
	"unsafe"
)

// StringToBytes converts string to a byte slice without any memory allocation.
func StringToBytes(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

// BytesToString converts byte slice to a string without any memory allocation.
func BytesToString(b []byte) string {
	return unsafe.String(unsafe.SliceData(b), len(b))
}

// Uint64ToBytes converts uint64 to a little-endian byte slice.
func Uint64ToBytes(n uint64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, n)
	return b
}

// BytesToUint64 converts a little-endian byte slice to uint64.
func BytesToUint64(b []byte) uint64 {
	return binary.LittleEndian.Uint64(b)
}

// Uint64ToBytesByBigEndian converts uint64 to a big-endian byte slice.
func Uint64ToBytesByBigEndian(n uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, n)
	return b
}

// BytesToUint64ByBigEndian converts a big-endian byte slice to uint64.
func BytesToUint64ByBigEndian(b []byte) uint64 {
	return binary.BigEndian.Uint64(b)
}

// Int64ToBytes converts int64 to a little-endian byte slice.
func Int64ToBytes(n int64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, uint64(n))
	return b
}

// BytesToInt64 converts a little-endian byte slice to int64.
func BytesToInt64(b []byte) int64 {
	return int64(binary.LittleEndian.Uint64(b))
}

// Uint32ToBytes converts uint32 to a little-endian byte slice.
func Uint32ToBytes(n uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, n)
	return b
}

// BytesToUint32 converts a little-endian byte slice to uint32.
func BytesToUint32(b []byte) uint32 {
	return binary.LittleEndian.Uint32(b)
}

// Uint16ToBytes converts uint16 to a little-endian byte slice.
func Uint16ToBytes(n uint16) []byte {
	b := make([]byte, 2)
	binary.LittleEndian.PutUint16(b, n)
	return b
}

// BytesToUint16 converts a little-endian byte slice to uint16.
func BytesToUint16(b []byte) uint16 {
	return binary.LittleEndian.Uint16(b)
}

// Uint16ToBytesByBigEndian converts uint16 to a big-endian byte slice.
func Uint16ToBytesByBigEndian(n uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, n)
	return b
}

// BytesToUint16ByBigEndian converts a big-endian byte slice to uint16.
func BytesToUint16ByBigEndian(b []byte) uint16 {
	return binary.BigEndian.Uint16(b)
}

// BytesToUint64Slice converts a byte slice to a uint64 slice without copying.
// It is the caller's responsibility to ensure proper alignment and length.
func BytesToUint64Slice(b []byte) []uint64 {
	if len(b) < 8 {
		return nil
	}
	return unsafe.Slice((*uint64)(unsafe.Pointer(unsafe.SliceData(b))), len(b)/8)
}
