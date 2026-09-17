package utils

import (
	"encoding/binary"
	"errors"
)

var ErrInvalidLength = errors.New("invalid byte length")

func StringToBytes(s string) []byte {
	return []byte(s)
}

func BytesToString(b []byte) string {
	return string(b)
}

func Uint64ToBytes(n uint64) []byte {
	bytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(bytes, n)
	return bytes
}

func BytesToUint64(bytes []byte) (uint64, error) {
	if len(bytes) != 8 {
		return 0, ErrInvalidLength
	}
	return binary.LittleEndian.Uint64(bytes), nil
}

func Uint64ToBytesByBigEndian(n uint64) []byte {
	bytes := make([]byte, 8)
	binary.BigEndian.PutUint64(bytes, n)
	return bytes
}

func BytesToUint64ByBigEndian(bytes []byte) (uint64, error) {
	if len(bytes) != 8 {
		return 0, ErrInvalidLength
	}
	return binary.BigEndian.Uint64(bytes), nil
}

func Int64ToBytes(n int64) []byte {
	return Uint64ToBytes(uint64(n))
}

func BytesToInt64(bytes []byte) (int64, error) {
	value, err := BytesToUint64(bytes)
	return int64(value), err
}

func Uint32ToBytes(n uint32) []byte {
	bytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(bytes, n)
	return bytes
}

func BytesToUint32(bytes []byte) (uint32, error) {
	if len(bytes) != 4 {
		return 0, ErrInvalidLength
	}
	return binary.LittleEndian.Uint32(bytes), nil
}

func Uint16ToBytes(n uint16) []byte {
	bytes := make([]byte, 2)
	binary.LittleEndian.PutUint16(bytes, n)
	return bytes
}

func BytesToUint16(bytes []byte) (uint16, error) {
	if len(bytes) != 2 {
		return 0, ErrInvalidLength
	}
	return binary.LittleEndian.Uint16(bytes), nil
}

func Uint16ToBytesByBigEndian(n uint16) []byte {
	bytes := make([]byte, 2)
	binary.BigEndian.PutUint16(bytes, n)
	return bytes
}

func BytesToUint16ByBigEndian(bytes []byte) (uint16, error) {
	if len(bytes) != 2 {
		return 0, ErrInvalidLength
	}
	return binary.BigEndian.Uint16(bytes), nil
}

func BytesToUint64Slice(bytes []byte) ([]uint64, error) {
	if len(bytes)%8 != 0 {
		return nil, ErrInvalidLength
	}
	values := make([]uint64, len(bytes)/8)
	for index := range values {
		offset := index * 8
		values[index] = binary.LittleEndian.Uint64(bytes[offset : offset+8])
	}
	return values, nil
}
