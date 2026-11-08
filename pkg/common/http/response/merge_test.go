package response

import "testing"

func TestMergeMapsResolvesConflictsAndAccumulatesValues(t *testing.T) {
	first := map[string]any{"shared": "first", "items": "a", "kept": 1}
	second := map[string]any{"shared": "second", "items": "b", "added": true}
	third := map[string]any{"items": "c"}

	for _, test := range []struct {
		name       string
		mode       MergeStrategy
		wantShared any
		wantItems  any
	}{
		{name: "first value wins", mode: MergeFirst, wantShared: "first", wantItems: "a"},
		{name: "last value wins", mode: MergeLast, wantShared: "second", wantItems: "c"},
		{name: "all values append", mode: MergeAppend, wantShared: []any{"first", "second"}, wantItems: []any{"a", "b", "c"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := MergeMaps(test.mode, first, nil, second, third)
			if test.mode != MergeAppend && got["shared"] != test.wantShared {
				t.Errorf("shared = %#v, want %#v", got["shared"], test.wantShared)
			}
			if test.mode == MergeAppend {
				shared, ok := got["shared"].([]any)
				if !ok || len(shared) != 2 || shared[0] != "first" || shared[1] != "second" {
					t.Errorf("shared = %#v, want both values", got["shared"])
				}
			}
			if test.mode == MergeAppend {
				values, ok := got["items"].([]any)
				if !ok || len(values) != 3 || values[0] != "a" || values[1] != "b" || values[2] != "c" {
					t.Errorf("items = %#v, want [a b c]", got["items"])
				}
			} else if got["items"] != test.wantItems {
				t.Errorf("items = %#v, want %#v", got["items"], test.wantItems)
			}
			if got["kept"] != 1 || got["added"] != true {
				t.Errorf("non-conflicting values were lost: %#v", got)
			}
		})
	}
}

func TestMergeStructsUsesConverterAndConflictPolicy(t *testing.T) {
	type source struct {
		Name string
		Tags []string
	}
	convert := func(value source) map[string]any {
		return map[string]any{"name": value.Name, "tags": value.Tags}
	}

	got := MergeStructs(MergeAppend, convert,
		source{Name: "profile", Tags: []string{"public"}},
		source{Name: "account", Tags: []string{"verified"}},
	)
	if names, ok := got["name"].([]any); !ok || len(names) != 2 || names[0] != "profile" || names[1] != "account" {
		t.Errorf("name = %#v, want values from both sources", got["name"])
	}
	tags, ok := got["tags"].([]any)
	if !ok || len(tags) != 2 || tags[0].([]string)[0] != "public" || tags[1].([]string)[0] != "verified" {
		t.Errorf("tags = %#v, want both source values", got["tags"])
	}
}
