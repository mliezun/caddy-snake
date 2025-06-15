package main

// #cgo pkg-config: python3-embed
// #include "experiment.c"
import "C"
import (
	_ "embed"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"
)

//go:embed experiment.py
var experiment string

type PythonMainThread struct {
	main chan func()
}

var pythonMainThreadOnce = sync.Once{}
var pythonMainThread *PythonMainThread = nil

func initPythonMainThread() {
	pythonMainThreadOnce.Do(func() {
		pythonMainThread = &PythonMainThread{
			main: make(chan func()),
		}
		go pythonMainThread.start()
	})
}

func (p *PythonMainThread) start() {
	runtime.LockOSThread()

	C.Py_init_experiment_v2(C.CString(experiment))

	for f := range p.main {
		f()
	}
}

func (p *PythonMainThread) do(f func()) {
	done := make(chan bool, 1)
	p.main <- func() {
		f()
		done <- true
	}
	<-done
}

func mainv3() {
	initPythonMainThread()
	time.Sleep(time.Minute)
}

func mainv2() {
	initPythonMainThread()
	// time.Sleep(time.Minute)
	sizes := []C.size_t{16, 64, 256, 1024, 4096, 4096 * 4, 4096 * 4 * 4, 4096 * 4 * 4 * 4}
	locks := make([]sync.Mutex, 4)
	var wg sync.WaitGroup
	for _, size := range sizes {
		start := time.Now()
		for ix := range 10000 {
			wg.Add(1)
			go func(ix int) {
				cBuf := (*C.char)(C.malloc(size))
				buffer := unsafe.Slice((*byte)(unsafe.Pointer(cBuf)), size)
				for i := range size {
					buffer[i] = byte('A' + i%26)
				}
				locks[ix%4].Lock()
				C.go_send_message(cBuf, size, C.int(ix))
				locks[ix%4].Unlock()
				C.free(unsafe.Pointer(cBuf))
				wg.Done()
			}(ix)
		}
		wg.Wait()
		fmt.Println("size", size, "duration", time.Since(start))
	}
}

func mainv1() {
	initPythonMainThread()
	// time.Sleep(time.Minute)
	var wg sync.WaitGroup
	sizes := []C.size_t{16, 64, 256, 1024, 4096, 4096 * 4, 4096 * 4 * 4, 4096 * 4 * 4 * 4}
	for _, size := range sizes {
		start := time.Now()
		for ix := range 10000 {
			wg.Add(1)
			go func(ix int) {
				cBuf := (*C.char)(C.malloc(size))
				buffer := unsafe.Slice((*byte)(unsafe.Pointer(cBuf)), size)
				for i := range size {
					buffer[i] = byte('A' + i%26)
				}
				msgTo := ix % 4
				switch msgTo {
				case 0:
					pythonMainThread.do(func() {
						C.send_message(cBuf, size)
					})
				case 1:
					pythonMainThread.do(func() {
						C.send_message2(cBuf, size)
					})
				case 2:
					pythonMainThread.do(func() {
						C.send_message3(cBuf, size)
					})
				case 3:
					pythonMainThread.do(func() {
						C.send_message4(cBuf, size)
					})
				}
				C.free(unsafe.Pointer(cBuf))
				wg.Done()
			}(ix)
		}
		wg.Wait()
		fmt.Println("size", size, "duration", time.Since(start))
	}
}

func main() {
	mainv2()
}
