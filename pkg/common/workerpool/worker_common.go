package workerpool

import "time"

// Worker is the interface for a worker that runs tasks.
type Worker interface {
	run()
	finish()
	lastUsedTime() time.Time
	setLastUsedTime(t time.Time)
	inputFunc(func())
	inputParam(any) // Added for Generic Pool support
}

// workerCommon contains common fields and methods for workers.
type workerCommon struct {
	lastUsed time.Time
}

// lastUsedTime returns the last used time of the worker.
func (w *workerCommon) lastUsedTime() time.Time {
	return w.lastUsed
}

// setLastUsedTime sets the last used time of the worker.
func (w *workerCommon) setLastUsedTime(t time.Time) {
	w.lastUsed = t
}
