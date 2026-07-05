// Package json is the single JSON entry point for go-common and its
// applications: bytedance/sonic for whole-struct codec, tidwall/gjson for
// reading single fields without decoding the whole document. Call sites read
// exactly like encoding/json — swapping the import is the whole migration,
// and swapping the engine later means touching only this package.
package json

import (
	stdjson "encoding/json"
	"io"

	"github.com/bytedance/sonic"
	"github.com/tidwall/gjson"
)

// Aliases to the stdlib contract types — sonic honors these interfaces, and
// values like RawMessage stay interchangeable with encoding/json code.
type (
	RawMessage  = stdjson.RawMessage
	Marshaler   = stdjson.Marshaler
	Unmarshaler = stdjson.Unmarshaler
)

// ── Whole-struct codec (sonic) ──

// Marshal returns the JSON encoding of v.
func Marshal(v any) ([]byte, error) {
	return sonic.Marshal(v)
}

// Unmarshal parses JSON data into v.
func Unmarshal(data []byte, v any) error {
	return sonic.Unmarshal(data, v)
}

// MarshalString is Marshal returning a string, skipping a []byte→string copy.
func MarshalString(v any) (string, error) {
	return sonic.MarshalString(v)
}

// UnmarshalString is Unmarshal for a string input, skipping a string→[]byte copy.
func UnmarshalString(data string, v any) error {
	return sonic.UnmarshalString(data, v)
}

// Valid reports whether data is well-formed JSON.
func Valid(data []byte) bool {
	return sonic.Valid(data)
}

// Encoder writes JSON values to an output stream.
type Encoder interface {
	Encode(v any) error
}

// Decoder reads JSON values from an input stream.
type Decoder interface {
	Decode(v any) error
}

// NewEncoder returns an Encoder writing to w.
func NewEncoder(w io.Writer) Encoder {
	return sonic.ConfigDefault.NewEncoder(w)
}

// NewDecoder returns a Decoder reading from r.
func NewDecoder(r io.Reader) Decoder {
	return sonic.ConfigDefault.NewDecoder(r)
}

// ── Partial extraction (gjson) ──
//
// Read one field from a large payload without a full unmarshal. Paths use
// dot notation: "user.address.city", "items.0.id", "items.#" (array length).
// See github.com/tidwall/gjson for the full path syntax.

// GetString returns the string at path and whether it exists.
func GetString(data []byte, path string) (string, bool) {
	r := gjson.GetBytes(data, path)
	return r.String(), r.Exists()
}

// GetInt returns the integer at path and whether it exists.
func GetInt(data []byte, path string) (int64, bool) {
	r := gjson.GetBytes(data, path)
	return r.Int(), r.Exists()
}

// GetFloat returns the float at path and whether it exists.
func GetFloat(data []byte, path string) (float64, bool) {
	r := gjson.GetBytes(data, path)
	return r.Float(), r.Exists()
}

// GetBool returns the boolean at path and whether it exists.
func GetBool(data []byte, path string) (bool, bool) {
	r := gjson.GetBytes(data, path)
	return r.Bool(), r.Exists()
}

// GetRaw returns the raw JSON of the value at path and whether it exists.
func GetRaw(data []byte, path string) ([]byte, bool) {
	r := gjson.GetBytes(data, path)
	return []byte(r.Raw), r.Exists()
}

// Exists reports whether a value exists at path.
func Exists(data []byte, path string) bool {
	return gjson.GetBytes(data, path).Exists()
}

// ForEach iterates the array or object at path, calling fn with each raw
// element (and its key for objects, "" for arrays). Return false to stop.
func ForEach(data []byte, path string, fn func(key string, value []byte) bool) {
	gjson.GetBytes(data, path).ForEach(func(k, v gjson.Result) bool {
		return fn(k.String(), []byte(v.Raw))
	})
}
