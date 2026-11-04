package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/huynhanx03/go-common/pkg/logger"
)

func TestTierCoordinatorHandlesConfiguredAndMissingEngines(t *testing.T) {
	ctx := context.Background()
	l1, l2 := newMemoryL1(), newMemoryL2()
	core, logs := observer.New(zap.DebugLevel)
	tiers := tierCoordinator{l1: l1, l2: l2, log: logger.Wrap(zap.New(core))}
	if _, ok := (tierCoordinator{}).getL2(ctx, "key", operationL2Get); ok {
		t.Fatal("missing L2 reported hit")
	}
	if _, ok := tiers.getL1(ctx, "key"); ok {
		t.Fatal("empty L1 value found")
	}
	if _, ok := (tierCoordinator{}).getL1(ctx, "key"); ok {
		t.Fatal("missing L1 reported hit")
	}
	l2.errors["get"] = ErrKeyNotFound
	if _, ok := tiers.getL2(ctx, "key", operationL2Get); ok {
		t.Fatal("missing L2 reported hit")
	}
	l2.errors["get"] = errors.New("backend")
	if _, ok := tiers.getL2(ctx, "key", operationL2Get); ok {
		t.Fatal("failed L2 reported hit")
	}
	delete(l2.errors, "get")
	l2.versions["key"] = "v1"
	l2.values["key"] = []byte("found")
	if value, ok := tiers.getL2(ctx, "key", operationL2Get); !ok || string(value) != "found" {
		t.Fatalf("L2 hit = %q, %v", value, ok)
	}
	if !tiers.snapshot(ctx, "key").l1.valid || !tiers.snapshot(ctx, "key").l2.valid {
		t.Fatal("configured tiers did not return versions")
	}
	if (tierCoordinator{}).snapshotL1(ctx, "key").valid || (tierCoordinator{}).snapshotL2(ctx, "key").valid {
		t.Fatal("missing tiers returned versions")
	}
	l1.errors["snapshot"] = errors.New("snapshot")
	if tiers.snapshotL1(ctx, "key").valid {
		t.Fatal("failed L1 snapshot was valid")
	}
	delete(l1.errors, "snapshot")
	l2.errors["snapshot"] = errors.New("snapshot")
	if tiers.snapshotL2(ctx, "key").valid {
		t.Fatal("failed L2 snapshot was valid")
	}
	delete(l2.errors, "snapshot")
	if _, _, ok := (tierCoordinator{}).getL2ForPromotion(ctx, "key", "key", operationL2Get); ok {
		t.Fatal("missing L2 promotion succeeded")
	}
	version := tierVersion{token: "v1", valid: true}
	tiers.setL1(ctx, "key", "local", 1, time.Minute, version, operationL1Set)
	tiers.writeL2(ctx, "key", "remote", []byte("v"), time.Minute, version, operationL2Set)
	invalid := tierVersion{}
	tiers.setL1(ctx, "key", "ignored", 1, time.Minute, invalid, operationL1Set)
	tiers.deleteL1(ctx, "key", "ignored", invalid, operationL1DeleteMissing)
	tiers.deleteL2(ctx, "key", "ignored", invalid, operationL2DeleteMissing)
	l1.errors["set"] = errors.New("set")
	l2.errors["set"] = errors.New("set")
	tiers.setL1(ctx, "key", "local", 1, time.Minute, version, operationL1Set)
	tiers.writeL2(ctx, "key", "remote", []byte("v"), time.Minute, version, operationL2Set)
	l1.errors["delete"] = errors.New("delete")
	l2.errors["delete"] = errors.New("delete")
	tiers.deleteL1(ctx, "key", "local", version, operationL1DeleteMissing)
	tiers.deleteL2(ctx, "key", "remote", version, operationL2DeleteMissing)
	storeEntryL2(ctx, tiers, "key", newEntry(make(chan int), time.Minute, 0), time.Minute, version)
	storeEntryL2(ctx, tiers, "key", newEntry("v", time.Minute, 0), time.Minute, invalid)
	if err := tiers.revoke(ctx, "key"); err != nil {
		t.Fatal(err)
	}
	if err := tiers.revoke(ctx, "key"); err != nil {
		t.Fatal(err)
	}
	if err := tiers.revokeMany(ctx, "key", "key"); err != nil {
		t.Fatal(err)
	}
	if err := (tierCoordinator{}).revoke(ctx, "key"); err != nil {
		t.Fatal(err)
	}
	if err := (tierCoordinator{}).revokeMany(ctx, "key"); err != nil {
		t.Fatal(err)
	}
	tiers.close()
	(tierCoordinator{}).close()
	if l1.closed.Load() != 1 || l2.closed.Load() != 1 {
		t.Fatalf("close counts = (%d, %d), want one per tier", l1.closed.Load(), l2.closed.Load())
	}
	tiers.storeNegative(ctx, "negative", time.Minute, tierVersions{l1: version, l2: version})
	tiers.storeNegative(ctx, "bad-negative", 0, tierVersions{l1: version, l2: version})
	before := logs.FilterMessage("cache operation failed").Len()
	tiers.reportError(ctx, "failure", errors.New("active error"))
	after := logs.FilterMessage("cache operation failed").Len()
	if after <= before {
		t.Fatal("active cache failure was not logged")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	tiers.reportError(canceled, "cancel", errors.New("ignored"))
	tiers.reportError(ctx, "nil", nil)
	if got := logs.FilterMessage("cache operation failed").Len(); got != after {
		t.Fatalf("canceled or nil errors logged: count %d, want %d", got, after)
	}
}
