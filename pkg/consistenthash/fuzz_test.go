package consistenthash

import "testing"

func FuzzReplaceAndLookup(f *testing.F) {
	f.Add("a", "b", []byte("key"))
	f.Add("worker-a", "worker-b", []byte{})
	f.Fuzz(func(t *testing.T, a, b string, key []byte) {
		r, err := New[string](Options{VirtualNodes: 4, MaxMemberIDBytes: 64, MaxTotalIDBytes: 128})
		if err != nil {
			t.Fatal(err)
		}
		err = r.Replace([]Member[string]{{ID: a}, {ID: b}})
		if err != nil {
			return
		}
		member, ok := r.Lookup(key)
		if !ok || member.ID == "" {
			t.Fatalf("invalid successful ring lookup")
		}
		out := r.LookupNInto(make([]Member[string], 0, 2), key, 2)
		for i := range out {
			for j := 0; j < i; j++ {
				if out[i].ID == out[j].ID {
					t.Fatal("duplicate fallback")
				}
			}
		}
	})
}
