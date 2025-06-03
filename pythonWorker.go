package caddysnake

// #cgo pkg-config: python3-embed
// #include "caddysnake.h"
import "C"
import (
	"runtime"
	"sync"
	"unsafe"
)

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

	setupPy := C.CString(caddysnake_py)
	defer C.free(unsafe.Pointer(setupPy))
	C.Py_init_and_release_gil(setupPy)

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
