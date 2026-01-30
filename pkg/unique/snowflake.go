package unique

import (
	"errors"
	"sync"

	"github.com/huynhanx03/go-common/pkg/settings"
	t "github.com/huynhanx03/go-common/pkg/timer"
)

// Node represents a Snowflake node
type SnowflakeNode struct {
	mu        sync.Mutex
	timestamp int64
	node      int64
	step      int64

	// Configuration
	epoch     int64
	nodeBits  uint8
	stepBits  uint8
	totalBits uint8

	// Pre-calculated masks and shifts
	nodeMax   int64
	stepMax   int64
	timeShift uint8
	nodeShift uint8
	limitMask int64

	// Dependencies
	clock t.Timer
}

func NewSnowflakeNode(config settings.SnowflakeNode, clock t.Timer) (*SnowflakeNode, error) {
	nodeMax := int64(-1 ^ (-1 << config.Config.Node))
	stepMax := int64(-1 ^ (-1 << config.Config.Step))

	if config.WorkerID < 0 || config.WorkerID > nodeMax {
		return nil, errors.New("node ID exceeds maximum allowed by configuration")
	}

	totalBits := config.Config.TotalBits
	if totalBits == 0 {
		totalBits = 63
	}

	// Safety check
	if totalBits <= config.Config.Node+config.Config.Step {
		return nil, errors.New("total bits must be greater than node + step bits")
	}

	// Calculate limit mask
	limitMask := int64(1)<<totalBits - 1
	if totalBits == 63 || totalBits == 64 {
		limitMask = int64(^uint64(0) >> 1)
	}

	return &SnowflakeNode{
		timestamp: 0,
		node:      config.WorkerID,
		step:      0,

		epoch:     config.Config.Epoch,
		nodeBits:  config.Config.Node,
		stepBits:  config.Config.Step,
		totalBits: totalBits,

		nodeMax:   nodeMax,
		stepMax:   stepMax,
		timeShift: config.Config.Node + config.Config.Step,
		nodeShift: config.Config.Step,
		limitMask: limitMask,

		clock: clock,
	}, nil
}

// Generate creates a unique ID
func (n *SnowflakeNode) Generate() int64 {
	n.mu.Lock()
	defer n.mu.Unlock()

	var now int64
	// Safety auto-switch to Seconds if total bits are tight (< 50)
	// 50 bits = ~35 years in millis, acceptable. < 50 bits risks quick overflow.
	if n.totalBits < 50 {
		now = n.clock.Now().Unix() // Use Seconds
	} else {
		now = n.clock.Now().UnixMilli() // Use Milliseconds
	}

	if now < n.timestamp {
		now = n.timestamp
	}

	if now == n.timestamp {
		n.step = (n.step + 1) & n.stepMax
		if n.step == 0 {
			for now <= n.timestamp {
				if n.totalBits < 50 {
					now = n.clock.Now().Unix()
				} else {
					now = n.clock.Now().UnixMilli()
				}
			}
		}
	} else {
		n.step = 0
	}

	n.timestamp = now

	id := ((now - n.epoch) << n.timeShift) | (n.node << n.nodeShift) | n.step
	return id & n.limitMask
}
