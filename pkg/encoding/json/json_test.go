package json

import (
	"bytes"
	"strings"
	"testing"
)

type sample struct {
	Name  string     `json:"name"`
	Age   int        `json:"age"`
	Tags  []string   `json:"tags,omitempty"`
	Extra RawMessage `json:"extra,omitempty"`
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	in := sample{Name: "jerry", Age: 30, Tags: []string{"go", "json"}, Extra: RawMessage(`{"k":1}`)}

	data, err := Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var out sample
	if err := Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.Name != in.Name || out.Age != in.Age || len(out.Tags) != 2 {
		t.Errorf("round trip mismatch: %+v", out)
	}
	if string(out.Extra) != `{"k":1}` {
		t.Errorf("RawMessage = %s, want {\"k\":1}", out.Extra)
	}
}

func TestMarshalStringUnmarshalString(t *testing.T) {
	s, err := MarshalString(map[string]int{"a": 1})
	if err != nil {
		t.Fatalf("MarshalString: %v", err)
	}

	var m map[string]int
	if err := UnmarshalString(s, &m); err != nil {
		t.Fatalf("UnmarshalString: %v", err)
	}
	if m["a"] != 1 {
		t.Errorf("m = %v", m)
	}
}

func TestEncoderDecoder(t *testing.T) {
	var buf bytes.Buffer
	if err := NewEncoder(&buf).Encode(sample{Name: "x"}); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	var out sample
	if err := NewDecoder(strings.NewReader(buf.String())).Decode(&out); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out.Name != "x" {
		t.Errorf("Name = %q, want x", out.Name)
	}
}

func TestValid(t *testing.T) {
	if !Valid([]byte(`{"a":1}`)) {
		t.Error("Valid(good) = false")
	}
	if Valid([]byte(`{"a":`)) {
		t.Error("Valid(bad) = true")
	}
}

func TestPartialGetters(t *testing.T) {
	data := []byte(`{"user":{"name":"jerry","age":30,"admin":true,"score":9.5},"items":[{"id":1},{"id":2}]}`)

	if v, ok := GetString(data, "user.name"); !ok || v != "jerry" {
		t.Errorf("GetString = %q, %v", v, ok)
	}
	if v, ok := GetInt(data, "user.age"); !ok || v != 30 {
		t.Errorf("GetInt = %d, %v", v, ok)
	}
	if v, ok := GetFloat(data, "user.score"); !ok || v != 9.5 {
		t.Errorf("GetFloat = %v, %v", v, ok)
	}
	if v, ok := GetBool(data, "user.admin"); !ok || !v {
		t.Errorf("GetBool = %v, %v", v, ok)
	}
	if v, ok := GetInt(data, "items.1.id"); !ok || v != 2 {
		t.Errorf("GetInt(array path) = %d, %v", v, ok)
	}
	if raw, ok := GetRaw(data, "user"); !ok || !Valid(raw) {
		t.Errorf("GetRaw = %s, %v", raw, ok)
	}
	if _, ok := GetString(data, "user.missing"); ok {
		t.Error("GetString(missing) reported exists")
	}
	if Exists(data, "nope") {
		t.Error("Exists(nope) = true")
	}
}

func TestForEach(t *testing.T) {
	data := []byte(`{"items":[10,20,30]}`)

	var sum int64
	ForEach(data, "items", func(_ string, value []byte) bool {
		v, _ := GetInt(value, "@this")
		sum += v
		return true
	})
	if sum != 60 {
		t.Errorf("sum = %d, want 60", sum)
	}
}
