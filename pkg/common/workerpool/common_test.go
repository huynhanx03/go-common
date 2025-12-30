package workerpool

import "time"

const (
	_   = 1 << (10 * iota)
	KiB // 1024
	MiB // 1048576
)

const (
	Param    = 100
	PoolSize = 1000
	TestSize = 10000
	n        = 100000
)

var curMem uint64

func demoFunc() {
	time.Sleep(10 * time.Millisecond)
}

func demoPoolFunc(args any) {
	time.Sleep(10 * time.Millisecond)
}

func demoPoolFuncInt(args int) {
	time.Sleep(10 * time.Millisecond)
}

func longRunningFunc() {
	time.Sleep(1 * time.Second)
}

type mockWorker struct {
	workerCommon
}

func (w *mockWorker) run()             {}
func (w *mockWorker) finish()          {}
func (w *mockWorker) inputFunc(func()) {}
func (w *mockWorker) inputParam(any)   {}
