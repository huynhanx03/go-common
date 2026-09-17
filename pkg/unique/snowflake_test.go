package unique

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/huynhanx03/go-common/pkg/settings"
)

type scriptedClock struct {
	mu     sync.Mutex
	values []int64
	index  int
}

func (clock *scriptedClock) Now() int64 {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	if clock.index >= len(clock.values) {
		return clock.values[len(clock.values)-1]
	}
	value := clock.values[clock.index]
	clock.index++
	return value
}

func (*scriptedClock) Stop() {}

func snowflakeConfig(totalBits, stepBits uint8) settings.SnowflakeNode {
	return settings.SnowflakeNode{
		Config: settings.Snowflake{
			Epoch:     1,
			Node:      1,
			Step:      stepBits,
			TotalBits: totalBits,
		},
		WorkerID: 1,
	}
}

func TestSnowflakeRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	clock := &scriptedClock{values: []int64{time.Now().UnixNano()}}
	for _, config := range []settings.SnowflakeNode{
		snowflakeConfig(63, 0),
		snowflakeConfig(1, 1),
		{Config: settings.Snowflake{Epoch: 1, Node: 1, Step: 1, TotalBits: 64}, WorkerID: 1},
	} {
		if _, err := NewSnowflakeNode(config, clock); !errors.Is(err, ErrInvalidSnowflakeConfig) {
			t.Fatalf("NewSnowflakeNode(%+v) error = %v", config, err)
		}
	}
	if _, err := NewSnowflakeNode(snowflakeConfig(63, 12), nil); !errors.Is(
		err,
		ErrInvalidSnowflakeConfig,
	) {
		t.Fatalf("nil clock error = %v", err)
	}
}

func TestSnowflakeReportsRollbackSequenceExhaustionAndOverflow(t *testing.T) {
	t.Parallel()

	rollbackClock := &scriptedClock{values: []int64{10_000_000, 9_000_000}}
	node, err := NewSnowflakeNode(snowflakeConfig(63, 2), rollbackClock)
	if err != nil {
		t.Fatalf("rollback node: %v", err)
	}
	if _, err := node.Generate(); err != nil {
		t.Fatalf("first Generate: %v", err)
	}
	if _, err := node.Generate(); !errors.Is(err, ErrClockMovedBackward) {
		t.Fatalf("rollback Generate error = %v", err)
	}

	exhaustedClock := &scriptedClock{values: []int64{10_000_000}}
	node, err = NewSnowflakeNode(snowflakeConfig(63, 1), exhaustedClock)
	if err != nil {
		t.Fatalf("exhausted node: %v", err)
	}
	for index := 0; index < 2; index++ {
		if _, err := node.Generate(); err != nil {
			t.Fatalf("Generate[%d]: %v", index, err)
		}
	}
	if _, err := node.Generate(); !errors.Is(err, ErrSequenceExhausted) {
		t.Fatalf("exhaustion error = %v", err)
	}

	overflowClock := &scriptedClock{values: []int64{1000 * int64(time.Second)}}
	node, err = NewSnowflakeNode(snowflakeConfig(8, 1), overflowClock)
	if err != nil {
		t.Fatalf("overflow node: %v", err)
	}
	if _, err := node.Generate(); !errors.Is(err, ErrTimestampOverflow) {
		t.Fatalf("overflow error = %v", err)
	}
}
