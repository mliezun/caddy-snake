package caddysnake

import (
	"bytes"
	"io"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// WorkerOutput is one line a Python worker wrote to stdout or stderr, or a
// notice from caddy-snake itself about that app (Stream "caddy-snake").
type WorkerOutput struct {
	// WorkingDir is the absolute working directory the worker was started in.
	WorkingDir string
	// App is the "module:variable" import path.
	App string
	// Stream is "stdout", "stderr" or "caddy-snake".
	Stream string
	Line   string
	Time   time.Time
}

var outputObservers struct {
	mu    sync.RWMutex
	next  int
	fns   map[int]func(WorkerOutput)
	count atomic.Int32
}

// ObserveWorkerOutput registers fn to receive every line written by local
// Python workers (tracebacks, print output, import errors), tagged with the
// app's working directory. It returns a function that removes the observer.
//
// fn is called from the goroutine copying the worker's output, so it must be
// fast and must not block. Output still goes to Caddy's own stdout/stderr.
// Workers running under Docker isolation are not observed.
func ObserveWorkerOutput(fn func(WorkerOutput)) (cancel func()) {
	o := &outputObservers
	o.mu.Lock()
	if o.fns == nil {
		o.fns = make(map[int]func(WorkerOutput))
	}
	id := o.next
	o.next++
	o.fns[id] = fn
	o.count.Store(int32(len(o.fns)))
	o.mu.Unlock()
	return func() {
		o.mu.Lock()
		delete(o.fns, id)
		o.count.Store(int32(len(o.fns)))
		o.mu.Unlock()
	}
}

func emitWorkerOutput(out WorkerOutput) {
	o := &outputObservers
	if o.count.Load() == 0 {
		return
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	for _, fn := range o.fns {
		fn(out)
	}
}

// maxObservedLine bounds how much of a single unterminated line is buffered.
const maxObservedLine = 64 << 10

// outputTee passes worker output through to dst unchanged and reports complete
// lines to the registered observers.
type outputTee struct {
	dst    io.Writer
	dir    string
	app    string
	stream string

	mu  sync.Mutex
	buf []byte
}

func newOutputTee(dst io.Writer, workingDir, app, stream string) *outputTee {
	if abs, err := filepath.Abs(workingDir); err == nil {
		workingDir = abs
	}
	return &outputTee{dst: dst, dir: workingDir, app: app, stream: stream}
}

func (t *outputTee) Write(p []byte) (int, error) {
	n, err := t.dst.Write(p)
	if outputObservers.count.Load() == 0 {
		return n, err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	for {
		i := bytes.IndexByte(t.buf, '\n')
		if i < 0 {
			if len(t.buf) > maxObservedLine {
				t.emit(t.buf)
				t.buf = t.buf[:0]
			}
			break
		}
		t.emit(t.buf[:i])
		t.buf = t.buf[i+1:]
	}
	if len(t.buf) == 0 {
		t.buf = nil
	}
	return n, err
}

func (t *outputTee) emit(line []byte) {
	line = bytes.TrimRight(line, "\r")
	emitWorkerOutput(WorkerOutput{
		WorkingDir: t.dir,
		App:        t.app,
		Stream:     t.stream,
		Line:       string(line),
		Time:       time.Now(),
	})
}
