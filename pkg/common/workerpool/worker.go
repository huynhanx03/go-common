package workerpool

var _ Worker = (*worker)(nil)

// worker runs the actual tasks
type worker struct {
	pool *Pool
	task chan func()
	workerCommon
}

// run starts the worker.
func (w *worker) run() {
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

		for fn := range w.task {
			if fn == nil {
				return
			}
			fn()
			if ok := w.pool.revertWorker(w); !ok {
				return
			}
		}
	}()
}

// finish signals the worker to exit.
func (w *worker) finish() {
	w.task <- nil
}

// inputFunc sends a function to the worker to execute.
func (w *worker) inputFunc(fn func()) {
	w.task <- fn
}

// inputParam sends a parameter to the worker to execute.
func (w *worker) inputParam(arg any) {
	panic("unreachable")
}
