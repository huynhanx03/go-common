package consistenthash

import "testing"

// These vectors freeze algorithm-v1 compatibility. Values are intentionally
// independent of member input order and Value payload.
func TestAlgorithmV1Golden(t *testing.T) {
	r := mustRing(t, Options{VirtualNodes: 4, CollisionAttempts: 8})
	if err := r.Replace([]Member[string]{
		{ID: "worker-c", Value: "ignored", Weight: 2},
		{ID: "worker-a", Value: "ignored"},
		{ID: "worker-b", Value: "ignored", Weight: 3},
	}); err != nil {
		t.Fatal(err)
	}
	const wantChecksum uint64 = 13435303017218821499
	if got := r.Checksum(); got != wantChecksum {
		t.Fatalf("checksum = %d", got)
	}
	for _, vector := range []struct {
		key  string
		want string
	}{
		{"tenant-1", "worker-c"}, {"tenant-2", "worker-b"}, {"tenant-3", "worker-b"}, {"forge/partition/42", "worker-b"},
	} {
		got, ok := r.Lookup([]byte(vector.key))
		if !ok {
			t.Fatal("missing member")
		}
		if got.ID != vector.want {
			t.Fatalf("key %q = %q", vector.key, got.ID)
		}
	}
}
