package workerpool

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/huynhanx03/go-common/pkg/common/locks"
)

const (
	// OPENED represents that the pool is opened.
	OPENED = iota

	// CLOSED represents that the pool is closed.
	CLOSED
)

const (
	NOT_DONE = iota
	DONE
)

const (
	// DefaultCleanIntervalTime is the interval time to clean up expired workers.
	DefaultCleanIntervalTime = time.Second

	// nowTimeUpdateInterval is the interval time to update the current time.
	nowTimeUpdateInterval = 500 * time.Millisecond
)

var (
	// workerChanCap determines whether the channel of a worker should be a buffered channel
	// to get the best performance. Inspired by fasthttp at
	// https://github.com/valyala/fasthttp/blob/master/workerpool.go#L139
	workerChanCap = func() int {
		// Use blocking channel if GOMAXPROCS=1.
		// This switches context from sender to receiver immediately,
		// which results in higher performance (under go1.5 at least).
		if runtime.GOMAXPROCS(0) == 1 {
			return 0
		}

		// Use non-blocking workerChan if GOMAXPROCS>1,
		// since otherwise the sender might be dragged down if the receiver is CPU-bound.
		return 1
	}()
)

// poolCommon contains all common fields for pool and generic pool.
type poolCommon struct {
	// capacity of the pool.
	capacity int32

	// running is the number of the currently running workers.
	running int32

	// state is the state of the pool (OPENED: open, CLOSED: closed).
	state int32

	// lock for synchronizing access to the worker queue.
	lock sync.Locker

	// cond for waiting for available workers or idle workers.
	cond *sync.Cond

	// workerCache for speeding up the creation of workers.
	workerCache sync.Pool

	// waiting is the number of goroutines waiting for a worker using blocking mode.
	waiting int32

	// purge is the context for purging expired workers.
	purgeDone int32
	purgeCtx  context.Context
	stopPurge context.CancelFunc

	// ticktock is the context for updating the current time.
	ticktockDone int32
	ticktockCtx  context.Context
	stopTicktock context.CancelFunc

	// now is the current time.
	now atomic.Value

	// workers is a slice that store the available workers.
	workers Queue

	// allDone is the channel that is closed when the pool is closed.
	allDone chan struct{}

	options *Options

	// once is used to ensure that the pool is closed only once.
	once *sync.Once
}

func newPoolCommon(size int, options ...Option) (*poolCommon, error) {
	opts := loadOptions(options...)

	if size <= 0 && size != -1 {
		return nil, ErrInvalidPoolSize
	}

	if opts.ExpiryDuration <= 0 {
		opts.ExpiryDuration = DefaultCleanIntervalTime
	}

	p := &poolCommon{
		capacity: int32(size),
		lock:     locks.NewSpinLock(),
		options:  opts,
		allDone:  make(chan struct{}),
		once:     &sync.Once{},
	}
	p.cond = sync.NewCond(p.lock)

	if opts.PreAlloc {
		if size == -1 {
			return nil, ErrInvalidPreAllocSize
		}
		p.workers = newQueue(QueueTypeFIFO, size)
	} else {
		p.workers = newQueue(QueueTypeLIFO, 0)
	}

	p.goPurge()
	p.goTicktock()

	return p, nil
}

// Running returns the number of the currently running workers.
func (p *poolCommon) Running() int {
	return int(atomic.LoadInt32(&p.running))
}

// Free returns the number of the free workers.
func (p *poolCommon) Free() int {
	c := p.Cap()
	if c < 0 {
		return -1
	}
	return c - p.Running()
}

// Waiting returns the number of the waiting workers.
func (p *poolCommon) Waiting() int {
	return int(atomic.LoadInt32(&p.waiting))
}

// Cap returns the capacity of the pool.
func (p *poolCommon) Cap() int {
	return int(atomic.LoadInt32(&p.capacity))
}

// Tune tunes the capacity of the pool.
func (p *poolCommon) Tune(size int) {
	capacity := p.Cap()
	if capacity == -1 || size <= 0 || size == capacity || p.options.PreAlloc {
		return
	}

	atomic.StoreInt32(&p.capacity, int32(size))

	if size > capacity {
		if size-capacity == 1 {
			p.cond.Signal()
		} else {
			p.cond.Broadcast()
		}
	}
}

// IsClosed returns true if the pool is closed.
func (p *poolCommon) IsClosed() bool {
	return atomic.LoadInt32(&p.state) == CLOSED
}

// incRunning increments the number of the currently running workers.
func (p *poolCommon) incRunning() int {
	return int(atomic.AddInt32(&p.running, 1))
}

// decRunning decrements the number of the currently running workers.
func (p *poolCommon) decRunning() int {
	return int(atomic.AddInt32(&p.running, -1))
}

func (p *poolCommon) addWaiting(delta int) {
	atomic.AddInt32(&p.waiting, int32(delta))
}

// purgeStaleWorkers purges expired workers periodically.
func (p *poolCommon) purgeStaleWorkers(ctx context.Context, workers Queue) {
	ticker := time.NewTicker(p.options.ExpiryDuration)
	defer func() {
		ticker.Stop()
		atomic.StoreInt32(&p.purgeDone, DONE)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if p.IsClosed() {
			break
		}

		p.lock.Lock()
		expiredWorkers := workers.refresh(p.options.ExpiryDuration)
		n := p.Running()
		isDormant := n == 0 || n == len(expiredWorkers)
		p.lock.Unlock()

		for i := range expiredWorkers {
			expiredWorkers[i].finish()
			expiredWorkers[i] = nil
		}

		if isDormant && p.Waiting() > 0 {
			p.cond.Broadcast()
		}
	}
}

// ticktock is a goroutine that updates the current time in the pool regularly.
func (p *poolCommon) ticktock(ctx context.Context) {
	ticker := time.NewTicker(nowTimeUpdateInterval)
	defer func() {
		ticker.Stop()
		atomic.StoreInt32(&p.ticktockDone, DONE)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if p.IsClosed() {
			break
		}

		p.now.Store(time.Now())
	}
}

// goPurge starts a goroutine to clean up expired workers periodically.
func (p *poolCommon) goPurge() {
	if p.options.DisablePurge {
		return
	}

	// Start a goroutine to clean up expired workers periodically.
	p.purgeCtx, p.stopPurge = context.WithCancel(context.Background())
	go p.purgeStaleWorkers(p.purgeCtx, p.workers)
}

// goTicktock starts a goroutine to update the current time in the pool regularly.
func (p *poolCommon) goTicktock() {
	p.now.Store(time.Now())
	p.ticktockCtx, p.stopTicktock = context.WithCancel(context.Background())
	go p.ticktock(p.ticktockCtx)
}

// nowTime returns the current time in the pool.
func (p *poolCommon) nowTime() time.Time {
	return p.now.Load().(time.Time)
}

// retrieveWorker returns an available worker to run the tasks.
func (p *poolCommon) retrieveWorker() (w Worker, err error) {
	p.lock.Lock()

retry:
	// First try to fetch the worker from the queue.
	if w = p.workers.detach(); w != nil {
		p.lock.Unlock()
		return
	}

	// If the worker queue is empty, and we don't run out of the pool capacity,
	// then just spawn a new worker goroutine.
	if capacity := p.Cap(); capacity == -1 || capacity > p.Running() {
		p.lock.Unlock()
		w = p.workerCache.Get().(Worker)
		w.run()
		return
	}

	// Bail out early if it's in nonblocking mode or the number of pending callers reaches the maximum limit value.
	if p.options.Nonblocking || (p.options.MaxBlockingTasks != 0 && p.Waiting() >= p.options.MaxBlockingTasks) {
		p.lock.Unlock()
		return nil, ErrPoolOverload
	}

	// Otherwise, we'll have to keep them blocked and wait for at least one worker to be put back into pool.
	p.addWaiting(1)
	p.cond.Wait() // block and wait for an available worker
	p.addWaiting(-1)

	if p.IsClosed() {
		p.lock.Unlock()
		return nil, ErrPoolClosed
	}

	goto retry
}

// revertWorker puts a worker back into free pool, recycling the goroutines.
func (p *poolCommon) revertWorker(worker Worker) bool {
	if capacity := p.Cap(); capacity > 0 && p.Running() > capacity || p.IsClosed() {
		p.cond.Broadcast()
		return false
	}

	worker.setLastUsedTime(p.nowTime())

	p.lock.Lock() // To avoid memory leaks, add a double check in the lock scope
	if p.IsClosed() {
		p.lock.Unlock()
		return false
	}

	if err := p.workers.insert(worker); err != nil {
		p.lock.Unlock()
		return false
	}

	p.cond.Signal()
	p.lock.Unlock()
	return true
}

// Release closes this pool and releases the worker queue.
func (p *poolCommon) Release() {
	if !atomic.CompareAndSwapInt32(&p.state, OPENED, CLOSED) {
		return
	}

	if p.stopPurge != nil {
		p.stopPurge()
		p.stopPurge = nil
	}
	if p.stopTicktock != nil {
		p.stopTicktock()
		p.stopTicktock = nil
	}

	p.lock.Lock()
	p.workers.reset()
	p.lock.Unlock()

	// Wake up the listeners stuck in retrieveWorker().
	p.cond.Broadcast()
}

// ReleaseTimeout is like Release but with a timeout, it waits all workers to exit before timing out.
func (p *poolCommon) ReleaseTimeout(timeout time.Duration) error {
	if p.IsClosed() || (!p.options.DisablePurge && p.stopPurge == nil) || p.stopTicktock == nil {
		return ErrPoolClosed
	}

	p.Release()

	var purgeCh <-chan struct{}
	if !p.options.DisablePurge {
		purgeCh = p.purgeCtx.Done()
	} else {
		purgeCh = p.allDone
	}

	if p.Running() == 0 {
		p.once.Do(func() {
			close(p.allDone)
		})
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return ErrTimeout
		case <-p.allDone:
			<-purgeCh
			<-p.ticktockCtx.Done()
			if p.Running() == 0 &&
				(p.options.DisablePurge || atomic.LoadInt32(&p.purgeDone) == DONE) &&
				atomic.LoadInt32(&p.ticktockDone) == DONE {
				return nil
			}
		}
	}
}

// Reboot reboots a closed pool.
func (p *poolCommon) Reboot() {
	if atomic.CompareAndSwapInt32(&p.state, CLOSED, OPENED) {
		atomic.StoreInt32(&p.purgeDone, NOT_DONE)
		p.goPurge()
		atomic.StoreInt32(&p.ticktockDone, NOT_DONE)
		p.goTicktock()
		p.allDone = make(chan struct{})
		p.once = &sync.Once{}
	}
}
