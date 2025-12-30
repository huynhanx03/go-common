package workerpool

var _ Worker = (*genericWorker[any])(nil)

// genericWorker runs the actual tasks with keys
type genericWorker[T any] struct {
	pool *GenericPool[T]
	arg  chan T
	exit chan struct{} // signal to exit
	workerCommon
}

func (w *genericWorker[T]) run() {
	w.pool.incRunning()
	go func() {
		defer func() {
			if w.pool.decRunning() == 0 && w.pool.IsClosed() {
				w.pool.once.Do(func() {
					close(w.pool.allDone)
				})
			}

			w.pool.workerCache.Put(w)
			if p := recover(); p != nil {
				if ph := w.pool.options.PanicHandler; ph != nil {
					ph(p)
				} else {
					// TODO: log
				}
			}
			// Signal the pool that the worker is available.
			w.pool.cond.Signal()
		}()

		for {
			select {
			case <-w.exit:
				return
			case task := <-w.arg:
				w.pool.fn(task)
				if ok := w.pool.revertWorker(w); !ok {
					return
				}
			}
		}
	}()
}

// finish signals the worker to exit.
func (w *genericWorker[T]) finish() {
	w.exit <- struct{}{}
}

// inputFunc sends a function to the worker to execute.
func (w *genericWorker[T]) inputFunc(fn func()) {
	panic("unreachable")
}

// inputParam sends a parameter to the worker to execute.
func (w *genericWorker[T]) inputParam(arg any) {
	w.arg <- arg.(T)
}
