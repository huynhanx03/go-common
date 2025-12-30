package workerpool

// GenericPool accepts the tasks and processes them via a pool of workers.
type GenericPool[T any] struct {
	*poolCommon
	fn func(T)
}

// NewGenericPool creates a new pool with function.
func NewGenericPool[T any](size int, pf func(T), options ...Option) (*GenericPool[T], error) {
	if pf == nil {
		return nil, ErrLackPoolFunc
	}

	if size <= 0 {
		return nil, ErrInvalidPoolSize
	}

	pc, err := newPoolCommon(size, options...)
	if err != nil {
		return nil, ErrInvalidPoolSize
	}

	p := &GenericPool[T]{
		poolCommon: pc,
		fn:         pf,
	}

	p.workerCache.New = func() any {
		return &genericWorker[T]{
			pool: p,
			arg:  make(chan T, workerChanCap),
			exit: make(chan struct{}, 1),
		}
	}

	return p, nil
}

func (p *GenericPool[T]) Invoke(arg T) error {
	if p.IsClosed() {
		return ErrPoolClosed
	}

	w, err := p.retrieveWorker()
	if w != nil {
		w.inputParam(arg)
	}
	return err
}
