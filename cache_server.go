package caddysnake

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Env vars passed to Python workers for cache access.
const (
	EnvCaddysnakeCacheAddr           = "CADDYSNAKE_CACHE_ADDR"
	EnvCaddysnakeWorkerInterface     = "CADDYSNAKE_WORKER_INTERFACE"
	EnvCaddysnakeCacheTimeoutSeconds = "CADDYSNAKE_CACHE_TIMEOUT"
)

// DefaultCacheClientTimeoutSec is a read/connect timeout hint for cache clients (seconds); client may ignore.
const DefaultCacheClientTimeoutSec = 30

// cacheAddrUnixScheme prefixes CADDYSNAKE_CACHE_ADDR when listening on a Unix domain socket.
const cacheAddrUnixScheme = "unix://"

// Resource limits (conservative defaults).
const (
	maxCacheKeyLen        = 8192
	maxCacheScalarLen     = 1 << 20 // 1 MiB
	maxCacheListElemLen   = 1 << 20
	maxCacheListLen       = 100_000
	maxCacheKeys          = 1_000_000
	maxRESPProtoLineBytes = 1 << 20
)

var errCacheLimit = errors.New("limit")

type entryKind int

const (
	entryScalar entryKind = iota
	entryList
)

type cacheEntry struct {
	kind   entryKind
	scalar []byte
	list   [][]byte
	expiry *time.Time // wall clock; nil = no expiry
}

func (e *cacheEntry) expired(now time.Time) bool {
	if e == nil || e.expiry == nil {
		return false
	}
	return !now.Before(*e.expiry)
}

type cacheStore struct {
	mu     sync.Mutex
	data   map[string]*cacheEntry
	conds  map[string]*sync.Cond // lazily created; all use &mu
	keyCap int                   // placeholder for max keys enforcement
}

func newCacheStore() *cacheStore {
	s := &cacheStore{
		data:  make(map[string]*cacheEntry),
		conds: make(map[string]*sync.Cond),
	}
	return s
}

func (s *cacheStore) condForKey(key string) *sync.Cond {
	if c, ok := s.conds[key]; ok {
		return c
	}
	c := sync.NewCond(&s.mu)
	s.conds[key] = c
	return c
}

func (s *cacheStore) dropCond(key string) {
	delete(s.conds, key)
}

func (s *cacheStore) broadcastKey(key string) {
	if c := s.conds[key]; c != nil {
		c.Broadcast()
	}
}

func (s *cacheStore) deleteEntryLocked(key string) {
	delete(s.data, key)
	s.dropCond(key)
}

func (s *cacheStore) checkKeyLen(key []byte) error {
	if len(key) == 0 || len(key) > maxCacheKeyLen {
		return fmt.Errorf("%w: key size", errCacheLimit)
	}
	return nil
}

func sanitizeErrMsg(msg string) string {
	msg = strings.ReplaceAll(msg, "\r", " ")
	msg = strings.ReplaceAll(msg, "\n", " ")
	return msg
}

func (s *cacheStore) checkScalarVal(v []byte) error {
	if len(v) > maxCacheScalarLen {
		return fmt.Errorf("%w: value size", errCacheLimit)
	}
	return nil
}

func (s *cacheStore) checkElem(v []byte) error {
	if len(v) > maxCacheListElemLen {
		return fmt.Errorf("%w: list element size", errCacheLimit)
	}
	return nil
}

func (s *cacheStore) enforceKeysCap() error {
	if len(s.data) >= maxCacheKeys {
		return fmt.Errorf("%w: too many keys", errCacheLimit)
	}
	return nil
}

func wallExpiry(ttlSec int64) *time.Time {
	if ttlSec <= 0 {
		return nil
	}
	t := time.Now().Add(time.Duration(ttlSec) * time.Second)
	return &t
}

// Set overwrites any prior value (scalar or list).
func (s *cacheStore) Set(key, value []byte, ttlSec int64) error {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkKeyLen(key); err != nil {
		return err
	}
	if err := s.checkScalarVal(value); err != nil {
		return err
	}

	k := string(key)
	if e := s.data[k]; e != nil && e.expired(now) {
		s.deleteEntryLocked(k)
		e = nil
	}
	if _, ok := s.data[k]; !ok {
		if err := s.enforceKeysCap(); err != nil {
			return err
		}
	}

	exp := wallExpiry(ttlSec)
	s.data[k] = &cacheEntry{kind: entryScalar, scalar: append([]byte(nil), value...), expiry: exp}
	s.broadcastKey(k) // wake pops if type changed
	return nil
}

// Get returns (scalar, nil, true, true) | (nil, listCopy, false, true) | (_, _, _, false) miss.
func (s *cacheStore) Get(key []byte) (scalar []byte, list [][]byte, isList, ok bool) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkKeyLen(key); err != nil {
		return nil, nil, false, false
	}
	k := string(key)
	e := s.data[k]
	if e == nil {
		return nil, nil, false, false
	}
	if e.expired(now) {
		s.deleteEntryLocked(k)
		return nil, nil, false, false
	}
	if e.kind == entryScalar {
		return append([]byte(nil), e.scalar...), nil, false, true
	}
	out := make([][]byte, len(e.list))
	for i, b := range e.list {
		out[i] = append([]byte(nil), b...)
	}
	return nil, out, true, true
}

func (s *cacheStore) Delete(key []byte) int {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkKeyLen(key); err != nil {
		return 0
	}
	k := string(key)
	e := s.data[k]
	if e == nil {
		return 0
	}
	if e.expired(now) {
		s.deleteEntryLocked(k)
		return 0
	}
	s.broadcastKey(k)
	s.deleteEntryLocked(k)
	return 1
}

func (s *cacheStore) Append(key, value []byte) error {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkKeyLen(key); err != nil {
		return err
	}
	if err := s.checkElem(value); err != nil {
		return err
	}
	k := string(key)
	e := s.data[k]
	if e != nil && e.expired(now) {
		s.deleteEntryLocked(k)
		e = nil
	}

	if e == nil {
		if err := s.enforceKeysCap(); err != nil {
			return err
		}
		if 1 > maxCacheListLen {
			return fmt.Errorf("%w: list length", errCacheLimit)
		}
		s.data[k] = &cacheEntry{
			kind:   entryList,
			list:   [][]byte{append([]byte(nil), value...)},
			expiry: nil,
		}
		s.broadcastKey(k)
		return nil
	}

	// clear TTL on append (per spec)
	noTTL := (*time.Time)(nil)
	if e.kind == entryScalar {
		if 2 > maxCacheListLen {
			return fmt.Errorf("%w: list length", errCacheLimit)
		}
		old := append([]byte(nil), e.scalar...)
		s.data[k] = &cacheEntry{
			kind:   entryList,
			list:   [][]byte{old, append([]byte(nil), value...)},
			expiry: noTTL,
		}
		s.broadcastKey(k)
		return nil
	}

	if len(e.list) >= maxCacheListLen {
		return fmt.Errorf("%w: list length", errCacheLimit)
	}
	e.list = append(e.list, append([]byte(nil), value...))
	e.expiry = noTTL
	s.broadcastKey(k)
	return nil
}

// Pop returns (value, true) | (nil, false) for immediate nil (miss, scalar, timeout, cancelled).
func (s *cacheStore) Pop(key []byte, deadline *time.Time) ([]byte, bool) {
	s.mu.Lock()

	if err := s.checkKeyLen(key); err != nil {
		s.mu.Unlock()
		return nil, false
	}
	k := string(key)

	timedOut := false
	var timer *time.Timer
	if deadline != nil {
		d := time.Until(*deadline)
		if d <= 0 {
			s.mu.Unlock()
			return nil, false
		}
		timer = time.AfterFunc(d, func() {
			s.mu.Lock()
			timedOut = true
			s.broadcastKey(k)
			s.mu.Unlock()
		})
	}

	for {
		now := time.Now()
		e := s.data[k]
		if e == nil || e.expired(now) {
			if e != nil {
				s.deleteEntryLocked(k)
			}
			if timer != nil {
				timer.Stop()
			}
			s.mu.Unlock()
			return nil, false
		}
		if e.kind == entryScalar {
			if timer != nil {
				timer.Stop()
			}
			s.mu.Unlock()
			return nil, false
		}
		if len(e.list) > 0 {
			v := e.list[0]
			e.list = e.list[1:]
			out := append([]byte(nil), v...)
			if timer != nil {
				timer.Stop()
			}
			s.mu.Unlock()
			return out, true
		}

		if timedOut {
			if timer != nil {
				timer.Stop()
			}
			s.mu.Unlock()
			return nil, false
		}

		cond := s.condForKey(k)
		cond.Wait()
	}
}

// --- RESP ---

func respWriteSimpleString(w *bufio.Writer, s string) error {
	if _, err := fmt.Fprintf(w, "+%s\r\n", s); err != nil {
		return err
	}
	return w.Flush()
}

func respWriteError(w *bufio.Writer, msg string) error {
	if _, err := fmt.Fprintf(w, "-ERR %s\r\n", sanitizeErrMsg(msg)); err != nil {
		return err
	}
	return w.Flush()
}

func respWriteInt(w *bufio.Writer, n int64) error {
	if _, err := fmt.Fprintf(w, ":%d\r\n", n); err != nil {
		return err
	}
	return w.Flush()
}

func respWriteBulk(w *bufio.Writer, b []byte) error {
	if b == nil {
		if _, err := io.WriteString(w, "$-1\r\n"); err != nil {
			return err
		}
		return w.Flush()
	}
	if _, err := fmt.Fprintf(w, "$%d\r\n", len(b)); err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "\r\n"); err != nil {
		return err
	}
	return w.Flush()
}

func respWriteBulkString(w *bufio.Writer, s string) error {
	return respWriteBulk(w, []byte(s))
}

func respWriteArrayHeader(w *bufio.Writer, n int) error {
	if _, err := fmt.Fprintf(w, "*%d\r\n", n); err != nil {
		return err
	}
	return nil
}

// respWriteArrayOfBulks writes *n followed by n bulk strings (no trailing flush until end).
func respWriteArrayOfBulks(w *bufio.Writer, elems [][]byte) error {
	if err := respWriteArrayHeader(w, len(elems)); err != nil {
		return err
	}
	for _, b := range elems {
		if _, err := fmt.Fprintf(w, "$%d\r\n", len(b)); err != nil {
			return err
		}
		if _, err := w.Write(b); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "\r\n"); err != nil {
			return err
		}
	}
	return w.Flush()
}

func respReadLine(r *bufio.Reader) ([]byte, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return nil, fmt.Errorf("invalid line ending")
	}
	if len(line)-2 > maxRESPProtoLineBytes {
		return nil, errCacheLimit
	}
	return line[:len(line)-2], nil
}

func respReadBulk(r *bufio.Reader) ([]byte, error) {
	line, err := respReadLine(r)
	if err != nil {
		return nil, err
	}
	if len(line) < 1 || line[0] != '$' {
		return nil, fmt.Errorf("expected bulk")
	}
	n, err := strconv.Atoi(string(line[1:]))
	if err != nil {
		return nil, err
	}
	if n == -1 {
		return nil, nil // null bulk in request? treat as empty
	}
	if n < 0 || n > maxCacheScalarLen {
		return nil, errCacheLimit
	}
	buf := make([]byte, n+2)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	if buf[n] != '\r' || buf[n+1] != '\n' {
		return nil, fmt.Errorf("bulk trailer")
	}
	return buf[:n], nil
}

func respReadArray(r *bufio.Reader) ([][]byte, error) {
	line, err := respReadLine(r)
	if err != nil {
		return nil, err
	}
	if len(line) < 1 || line[0] != '*' {
		return nil, fmt.Errorf("expected array")
	}
	n, err := strconv.Atoi(string(line[1:]))
	if err != nil || n < 1 || n > 32 {
		return nil, fmt.Errorf("bad array len")
	}
	out := make([][]byte, n)
	for i := 0; i < n; i++ {
		b, err := respReadBulk(r)
		if err != nil {
			return nil, err
		}
		out[i] = b
	}
	return out, nil
}

// --- IPC server (Unix socket on unix-like OS; loopback TCP on Windows) ---

type cacheServer struct {
	store   *cacheStore
	ln      net.Listener
	done    chan struct{}
	closeMu sync.Mutex
	closed  bool
	// addr is CADDYSNAKE_CACHE_ADDR: "unix://" + filesystem path, or "127.0.0.1:<port>" on Windows.
	addr    string
	sockDir string // non-empty when using a Unix socket — removed on Close
}

func startCacheServer() (*cacheServer, error) {
	if runtime.GOOS == "windows" {
		return startCacheServerTCPOnly()
	}
	return startCacheServerUnixSocket()
}

func startCacheServerTCPOnly() (*cacheServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &cacheServer{
		store: newCacheStore(),
		ln:    ln,
		done:  make(chan struct{}),
		addr:  ln.Addr().String(),
	}
	go s.acceptLoop()
	return s, nil
}

func startCacheServerUnixSocket() (*cacheServer, error) {
	dir, err := os.MkdirTemp("", "cs-*")
	if err != nil {
		return nil, err
	}
	if chErr := os.Chmod(dir, 0o700); chErr != nil {
		os.RemoveAll(dir)
		return nil, chErr
	}
	path := filepath.Join(dir, "cache.sock")
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		ln.Close()
		os.RemoveAll(dir)
		return nil, err
	}
	envAddr := cacheAddrUnixScheme + filepath.ToSlash(absPath)
	s := &cacheServer{
		store:   newCacheStore(),
		ln:      ln,
		done:    make(chan struct{}),
		addr:    envAddr,
		sockDir: dir,
	}
	go s.acceptLoop()
	return s, nil
}

func (s *cacheServer) Addr() string { return s.addr }

func (s *cacheServer) Close() error {
	s.closeMu.Lock()
	if s.closed {
		s.closeMu.Unlock()
		return nil
	}
	s.closed = true
	s.closeMu.Unlock()
	err := s.ln.Close()
	<-s.done
	if s.sockDir != "" {
		_ = os.RemoveAll(s.sockDir)
	}
	return err
}

func (s *cacheServer) acceptLoop() {
	defer close(s.done)
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetNoDelay(true)
		}
		go s.handleConn(conn)
	}
}

func (s *cacheServer) handleConn(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	for {
		parts, err := respReadArray(r)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			_ = respWriteError(w, err.Error())
			return
		}
		if len(parts) == 0 {
			_ = respWriteError(w, "empty command")
			return
		}
		cmd := strings.ToUpper(string(parts[0]))
		switch cmd {
		case "CSQUIT":
			_ = respWriteSimpleString(w, "OK")
			return
		case "CSGET":
			if len(parts) != 2 {
				_ = respWriteError(w, "wrong number of arguments for CSGET")
				return
			}
			scalar, list, isList, ok := s.store.Get(parts[1])
			if !ok {
				_ = respWriteBulk(w, nil) // $-1
				continue
			}
			if !isList {
				_ = respWriteBulk(w, scalar)
				continue
			}
			if len(list) == 0 {
				if err := respWriteArrayHeader(w, 0); err != nil {
					return
				}
				_ = w.Flush()
				continue
			}
			_ = respWriteArrayOfBulks(w, list)
		case "CSDEL":
			if len(parts) != 2 {
				_ = respWriteError(w, "wrong number of arguments for CSDEL")
				return
			}
			n := s.store.Delete(parts[1])
			_ = respWriteInt(w, int64(n))
		case "CSSET":
			if len(parts) != 3 && len(parts) != 4 {
				_ = respWriteError(w, "wrong number of arguments for CSSET")
				return
			}
			var ttl int64
			if len(parts) == 4 && len(parts[3]) > 0 {
				t, err := strconv.ParseInt(string(parts[3]), 10, 64)
				if err != nil || t < 0 {
					_ = respWriteError(w, "invalid TTL")
					return
				}
				ttl = t
			}
			if err := s.store.Set(parts[1], parts[2], ttl); err != nil {
				_ = respWriteError(w, err.Error())
				continue
			}
			_ = respWriteSimpleString(w, "OK")
		case "CSAPPEND":
			if len(parts) != 3 {
				_ = respWriteError(w, "wrong number of arguments for CSAPPEND")
				return
			}
			if err := s.store.Append(parts[1], parts[2]); err != nil {
				_ = respWriteError(w, err.Error())
				continue
			}
			_ = respWriteSimpleString(w, "OK")
		case "CSPOP":
			if len(parts) != 2 && len(parts) != 3 {
				_ = respWriteError(w, "wrong number of arguments for CSPOP")
				return
			}
			var dl *time.Time
			if len(parts) == 3 && len(parts[2]) > 0 {
				sec, err := strconv.ParseFloat(string(parts[2]), 64)
				if err != nil || sec < 0 {
					_ = respWriteError(w, "invalid timeout")
					return
				}
				t := time.Now().Add(time.Duration(sec * float64(time.Second)))
				dl = &t
			}
			v, ok := s.store.Pop(parts[1], dl)
			if !ok {
				_ = respWriteBulk(w, nil)
				continue
			}
			_ = respWriteBulk(w, v)
		default:
			_ = respWriteError(w, fmt.Sprintf("unknown command %q", cmd))
			return
		}
	}
}
