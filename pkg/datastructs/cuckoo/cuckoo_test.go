package cuckoo

import (
	"math/rand"
	"testing"
)

func TestFailedInsertionPreservesStoredItems(t *testing.T) {
	filter := New(4) // one four-slot bucket, so the fifth distinct item cannot fit
	filter.rnd = rand.New(rand.NewSource(2))
	items := []string{"alpha", "bravo", "charlie", "delta"}
	for _, item := range items {
		if err := filter.Add(item); err != nil {
			t.Fatalf("Add(%q): %v", item, err)
		}
	}
	if err := filter.Add("echo"); err == nil {
		t.Fatal("insertion into a full filter succeeded")
	}
	for _, item := range items {
		if !filter.Contains(item) {
			t.Errorf("failed insertion lost previously stored item %q", item)
		}
	}
	if filter.Count() != uint(len(items)) {
		t.Fatalf("count after failed insertion = %d, want %d", filter.Count(), len(items))
	}
}

func TestFilterTracksRepeatedItemsAcrossDeletion(t *testing.T) {
	filter := New(64)
	for range 2 {
		if err := filter.Add("same"); err != nil {
			t.Fatal(err)
		}
	}
	if filter.Count() != 2 || !filter.Delete("same") || !filter.Contains("same") {
		t.Fatal("deleting one repeated item removed both stored copies")
	}
	if !filter.Delete("same") || filter.Contains("same") || filter.Delete("same") {
		t.Fatal("delete did not remove exactly one copy or reported a missing item")
	}
	if err := filter.Add("same"); err != nil || !filter.Contains("same") {
		t.Fatalf("insert after deletion failed: %v", err)
	}
}
