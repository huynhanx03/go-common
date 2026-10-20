package unique

import (
	"errors"
	"math"
	"sync"

	"github.com/huynhanx03/go-common/pkg/settings"
	t "github.com/huynhanx03/go-common/pkg/timer"
)

var (
	ErrInvalidSnowflakeConfig = errors.New("invalid snowflake configuration")
	ErrClockMovedBackward     = errors.New("snowflake clock moved backward")
	ErrSequenceExhausted      = errors.New("snowflake sequence exhausted")
	ErrTimestampOverflow      = errors.New("snowflake timestamp overflow")
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
	timeMax   int64

	// Dependencies
	clock t.Timer
}

func NewSnowflakeNode(config settings.SnowflakeNode, clock t.Timer) (*SnowflakeNode, error) {
	if clock == nil ||
		config.Config.Node == 0 ||
		config.Config.Step == 0 ||
		config.Config.Node >= 63 ||
		config.Config.Step >= 63 ||
		config.Config.Epoch < 0 {
		return nil, ErrInvalidSnowflakeConfig
	}
	nodeMax := int64((uint64(1) << config.Config.Node) - 1)
	stepMax := int64((uint64(1) << config.Config.Step) - 1)

	if config.WorkerID < 0 || config.WorkerID > nodeMax {
		return nil, errors.Join(
			ErrInvalidSnowflakeConfig,
			errors.New("worker ID outside configured node bits"),
		)
	}

	totalBits := config.Config.TotalBits
	if totalBits == 0 {
		totalBits = 63
	}

	if totalBits > 63 ||
		totalBits <= config.Config.Node+config.Config.Step {
		return nil, ErrInvalidSnowflakeConfig
	}
	timeBits := totalBits - config.Config.Node - config.Config.Step
	if timeBits == 0 || timeBits >= 63 {
		return nil, ErrInvalidSnowflakeConfig
	}
	timeMax := int64((uint64(1) << timeBits) - 1)

	return &SnowflakeNode{
		timestamp: -1,
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
		timeMax:   timeMax,

		clock: clock,
	}, nil
}

// Generate creates a unique ID
func (n *SnowflakeNode) Generate() (int64, error) {
	if n == nil || n.clock == nil {
		return 0, ErrInvalidSnowflakeConfig
	}
	n.mu.Lock()
	defer n.mu.Unlock()

	var now int64
	nanos := n.clock.Now()
	// Safety auto-switch to Seconds if total bits are tight (< 50)
	// 50 bits = ~35 years in millis, acceptable. < 50 bits risks quick overflow.
	if n.totalBits < 50 {
		now = nanos / 1e9 // Seconds
	} else {
		now = nanos / 1e6 // Milliseconds
	}

	if now < n.timestamp {
		return 0, ErrClockMovedBackward
	}

	if now == n.timestamp {
		if n.step >= n.stepMax {
			return 0, ErrSequenceExhausted
		}
		n.step++
	} else {
		n.step = 0
	}

	elapsed := now - n.epoch
	if elapsed < 0 || elapsed > n.timeMax {
		return 0, ErrTimestampOverflow
	}
	n.timestamp = now

	id := (elapsed << n.timeShift) | (n.node << n.nodeShift) | n.step
	if id < 0 || id > math.MaxInt64 {
		return 0, ErrTimestampOverflow
	}
	return id, nil
}
