package consumer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"
)

type request struct {
	ctx      context.Context
	blocked  map[string]streamFailure // Protected by Runtime.mu.
	admitted bool
}

type streamFailure struct {
	index int
	err   error
}

type task struct {
	message  Message
	request  *request
	key      string
	priority int
	index    int
	previous *task
	finished bool
	result   Result
	done     chan struct{}
}

// Process admits messages and returns results in input order. A Retry stops
// later messages in the same ordered stream within this call. Sources should
// keep one outstanding Process call per source partition. Priority is limited
// to admitted messages. Canceled calls may return before a handler cooperates.
func (r *Runtime) Process(ctx context.Context, messages []Message) []Result {
	results := make([]Result, len(messages))
	if len(messages) == 0 {
		return results
	}
	group := &request{ctx: ctx}
	stopWake := context.AfterFunc(ctx, r.wake)
	defer stopWake()
	tasks := make([]task, len(messages))
	for i, message := range messages {
		item := &tasks[i]
		item.request = group
		item.index = i
		if r.config.ordering != nil {
			item.key = r.config.ordering(message)
		}
		if r.config.priority != nil {
			item.priority = r.config.priority(message)
		}
		if err := r.admit(ctx, item, message); err != nil {
			results[i] = Result{Outcome: Retry, Err: err}
			continue
		}
	}
	r.mu.Lock()
	group.admitted = true
	r.changed.Broadcast()
	r.mu.Unlock()
	for i := range tasks {
		item := &tasks[i]
		if item.done == nil {
			continue
		}
		select {
		case <-item.done:
			results[i] = item.result
		case <-ctx.Done():
			results[i] = Result{Outcome: Retry, Err: ctx.Err()}
		}
	}
	return results
}

func (r *Runtime) admit(ctx context.Context, item *task, message Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.closed {
			return ErrClosed
		}
		if err := blockedError(item); err != nil {
			return err
		}
		if len(r.queue) < r.config.queueCapacity {
			break
		}
		if r.config.fullQueue == Reject {
			r.block(item, ErrQueueFull)
			return ErrQueueFull
		}
		r.changed.Wait()
	}
	item.message = ownMessage(message)
	item.done = make(chan struct{})
	if item.key != "" {
		item.previous, r.tails[item.key] = r.tails[item.key], item
	}
	r.queue = append(r.queue, item)
	r.changed.Broadcast()
	return nil
}

func (r *Runtime) work() {
	for {
		batch := r.takeBatch()
		if len(batch) == 0 {
			return
		}
		results := r.execute(batch)
		r.mu.Lock()
		for i, item := range batch {
			r.finish(item, results[i])
		}
		r.running -= len(batch)
		r.changed.Broadcast()
		r.mu.Unlock()
	}
}

func (r *Runtime) takeBatch() []*task {
	r.mu.Lock()
	defer r.mu.Unlock()
	var batch []*task
	var group *request
	var deadline time.Time
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		index := r.next(group)
		if index >= 0 {
			item := r.queue[index]
			copy(r.queue[index:], r.queue[index+1:])
			r.queue[len(r.queue)-1] = nil
			r.queue = r.queue[:len(r.queue)-1]
			r.changed.Broadcast()
			if err := r.taskError(item); err != nil {
				r.finish(item, Result{Outcome: Retry, Err: err})
				continue
			}
			batch = append(batch, item)
			r.running++
			if len(batch) == r.config.batchSize {
				return batch
			}
			if group == nil {
				group = item.request
				deadline = time.Now().Add(r.config.batchWait)
				if r.config.batchWait > 0 {
					timer = time.AfterFunc(r.config.batchWait, r.wake)
				}
			}
			continue
		}
		if len(batch) > 0 && (r.closed || group.admitted || len(r.queue) == r.config.queueCapacity || group.ctx.Err() != nil || !time.Now().Before(deadline)) {
			return batch
		}
		if len(batch) == 0 && r.closed && len(r.queue) == 0 {
			return nil
		}
		r.changed.Wait()
	}
}

// next selects the oldest ready task, or a higher-priority task within the
// configured burst. Dependency links keep each ordering key FIFO.
func (r *Runtime) next(group *request) int {
	oldest, preferred := -1, -1
	for i, item := range r.queue {
		if group != nil && item.request != group {
			continue
		}
		if r.taskError(item) == nil && dependency(item) != nil {
			continue
		}
		if r.config.priority == nil || r.burst >= r.config.priorityBurst {
			r.burst = 0
			return i
		}
		if oldest < 0 {
			oldest, preferred = i, i
		}
		if item.priority > r.queue[preferred].priority {
			preferred = i
		}
	}
	if oldest < 0 {
		return -1
	}
	if preferred == oldest {
		r.burst = 0
		return oldest
	}
	r.burst++
	return preferred
}

func (r *Runtime) taskError(item *task) error {
	if err := item.request.ctx.Err(); err != nil {
		return err
	}
	if err := r.ctx.Err(); err != nil {
		return err
	}
	if item.key != "" {
		return blockedError(item)
	}
	return nil
}

func (r *Runtime) finish(item *task, result Result) {
	item.result, item.finished = result, true
	item.message = Message{}
	if item.key != "" && r.tails[item.key] == item {
		if previous := dependency(item); previous != nil {
			r.tails[item.key] = previous
		} else {
			delete(r.tails, item.key)
		}
	}
	if result.Outcome == Retry {
		r.block(item, errors.Join(ErrPrecedingMessageFailed, result.Err))
	}
	close(item.done)
}

// Canceled queued tasks can finish before their predecessor. Preserve the
// unfinished link so their followers cannot bypass a still-running handler.
func dependency(item *task) *task {
	for item.previous != nil && item.previous.finished {
		item.previous = item.previous.previous
	}
	return item.previous
}

func (r *Runtime) block(item *task, err error) {
	if item.key == "" {
		return
	}
	previous, exists := item.request.blocked[item.key]
	if !exists || item.index < previous.index {
		if item.request.blocked == nil {
			item.request.blocked = make(map[string]streamFailure)
		}
		item.request.blocked[item.key] = streamFailure{index: item.index, err: err}
	}
}

func blockedError(item *task) error {
	if failure, exists := item.request.blocked[item.key]; item.key != "" && exists && item.index > failure.index {
		return failure.err
	}
	return nil
}

func (r *Runtime) execute(batch []*task) (results []Result) {
	ctx, cancel := context.WithCancel(batch[0].request.ctx)
	stopCancel := context.AfterFunc(r.ctx, cancel)
	defer stopCancel()
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			results = retryResults(len(batch), fmt.Errorf("%w: %v\n%s", ErrHandlerPanic, recovered, debug.Stack()))
		}
	}()
	if err := ctx.Err(); err != nil {
		return retryResults(len(batch), err)
	}
	if r.handler != nil {
		message := batch[0].message
		outcome, err := r.handler(MessageContext(ctx, message), message)
		results = []Result{{Outcome: outcome, Err: err}}
	} else {
		messages := make([]Message, len(batch))
		for i, item := range batch {
			messages[i] = item.message
		}
		results = r.batchHandler(ctx, messages)
	}
	if err := ctx.Err(); err != nil {
		return retryResults(len(batch), err)
	}
	if len(results) != len(batch) {
		return retryResults(len(batch), ErrBatchResultCount)
	}
	for i, result := range results {
		switch result.Outcome {
		case Success:
			if result.Err != nil {
				results[i].Outcome = Retry
			}
		case Retry:
			if result.Err == nil {
				results[i].Err = ErrRetryRequested
			}
		case DeadLetter:
		default:
			results[i] = Result{Outcome: Retry, Err: errors.Join(ErrInvalidOutcome, result.Err)}
		}
	}
	return results
}

func retryResults(count int, err error) []Result {
	results := make([]Result, count)
	for i := range results {
		results[i] = Result{Outcome: Retry, Err: err}
	}
	return results
}

func ownMessage(message Message) Message {
	message.Key, message.Value = bytes.Clone(message.Key), bytes.Clone(message.Value)
	if message.Headers != nil {
		headers := make([]Header, len(message.Headers))
		for i, header := range message.Headers {
			headers[i] = Header{Key: header.Key, Value: bytes.Clone(header.Value)}
		}
		message.Headers = headers
	}
	return message
}
