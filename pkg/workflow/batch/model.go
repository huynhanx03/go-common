package batch

import (
	"context"
	"time"

	"github.com/huynhanx03/go-common/pkg/coordination"
)

// Status describes the durable state of one logical batch key.
type Status string

// Definition is the immutable identity and cutoff for one logical batch run.
type Definition struct {
	Key              string
	InputFingerprint string
	CursorSchema     string
	Boundary         []byte
}

// Checkpoint is the committed progress and current claim for one batch key.
type Checkpoint struct {
	Definition     Definition
	Cursor         []byte
	Version        int64
	LastStepID     string
	ProcessedCount int64
	Status         Status
	Owner          string
	Fence          coordination.FenceToken
	LeaseUntil     time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	CompletedAt    *time.Time
}

// Progress describes the next cursor and committed item count for one page.
type Progress struct {
	Cursor    []byte
	Processed int64
	Done      bool
}

// Result summarizes the last checkpoint committed by one Run segment.
type Result struct {
	Key            string
	Version        int64
	ProcessedCount int64
	Done           bool
}

// Step performs one database page using the transaction context supplied by Store.Apply.
type Step func(context.Context, Checkpoint) (Progress, error)

// StepRequest identifies a page attempt and the fenced claim that owns it.
type StepRequest struct {
	Grant           coordination.Grant
	ExpectedVersion int64
	StepID          string
}
