package workerpool

// Pool accepts the tasks and processes them via a pool of workers.
type Pool struct {
	*poolCommon
}

// NewPool creates a new pool.
func NewPool(size int, options ...Option) (*Pool, error) {
	pc, err := newPoolCommon(size, options...)
	if err != nil {
		return nil, ErrInvalidPoolSize
	}

	p := &Pool{
		poolCommon: pc,
	}

	p.workerCache.New = func() any {
		return &worker{
			pool: p,
			task: make(chan func(), workerChanCap),
		}
	}

	return p, nil
}

func (p *Pool) Submit(task func()) error {
	if p.IsClosed() {
		return ErrPoolClosed
	}

	w, err := p.retrieveWorker()
	if w != nil {
		w.inputFunc(task)
	}
	return err
}
