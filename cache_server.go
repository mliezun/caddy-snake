package caddysnake

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Env vars passed to Python workers for cache access.
const (
	EnvCaddysnakeCacheAddr           = "CADDYSNAKE_CACHE_ADDR"
	EnvCaddysnakeWorkerInterface     = "CADDYSNAKE_WORKER_INTERFACE"
	EnvCaddysnakeWorkerID            = "CADDYSNAKE_WORKER_ID"
	EnvCaddysnakeCacheTimeoutSeconds = "CADDYSNAKE_CACHE_TIMEOUT"
)

// DefaultCacheClientTimeoutSec is a read/connect timeout hint for cache clients (seconds); client may ignore.
const DefaultCacheClientTimeoutSec = 30

// cacheAddrUnixScheme prefixes CADDYSNAKE_CACHE_ADDR when listening on a Unix domain socket.
const cacheAddrUnixScheme = "unix://"

// Resource limits (conservative defaults).
const (
	maxCacheKeyLen         = 8192
	maxCacheScalarLen      = 1 << 20 // 1 MiB
	maxCacheListElemLen    = 1 << 20
	maxCacheListLen        = 100_000
	maxCacheKeys           = 1_000_000
	maxRESPProtoLineBytes  = 1 << 20
	defaultCSKEYSLimit     = 1000
	maxCSKEYSLimit         = 1000
	maxSubscribeTimeoutSec = 300.0
)

var (
	errCacheLimit = errors.New("limit")
	errWrongType  = errors.New("wrong type")
)

type entryKind int

const (
	entryScalar entryKind = iota
	entryList
	entrySet
)

type cacheEntry struct {
	kind    entryKind
	scalar  []byte
	list    [][]byte
	members map[string][]byte // set: dedup by string(member bytes)
	expiry  *time.Time        // wall clock; nil = no expiry
}

func (e *cacheEntry) expired(now time.Time) bool {
	if e == nil || e.expiry == nil {
		return false
	}
	return !now.Before(*e.expiry)
}

type cacheStore struct {
	mu      sync.Mutex
	data    map[string]*cacheEntry
	conds   map[string]*sync.Cond // lazily created; all use &mu
	closing bool
	keyCap  int //nolint:unused // reserved for future max-keys enforcement
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

func (s *cacheStore) shutdownLocked() {
	s.closing = true
	for _, c := range s.conds {
		c.Broadcast()
	}
}

func (s *cacheStore) Shutdown() {
	s.mu.Lock()
	s.shutdownLocked()
	s.mu.Unlock()
}

func (s *cacheStore) entryLocked(k string, now time.Time) (*cacheEntry, bool) {
	e := s.data[k]
	if e == nil {
		return nil, false
	}
	if e.expired(now) {
		s.deleteEntryLocked(k)
		return nil, false
	}
	return e, true
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

// Get returns value data and kind. ok=false on miss or invalid key.
func (s *cacheStore) Get(key []byte) (scalar []byte, list [][]byte, setMembers [][]byte, kind entryKind, ok bool) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkKeyLen(key); err != nil {
		return nil, nil, nil, entryScalar, false
	}
	k := string(key)
	e, ok := s.entryLocked(k, now)
	if !ok {
		return nil, nil, nil, entryScalar, false
	}
	switch e.kind {
	case entryScalar:
		return append([]byte(nil), e.scalar...), nil, nil, entryScalar, true
	case entryList:
		out := make([][]byte, len(e.list))
		for i, b := range e.list {
			out[i] = append([]byte(nil), b...)
		}
		return nil, out, nil, entryList, true
	case entrySet:
		out := setMembersSorted(e.members)
		return nil, nil, out, entrySet, true
	default:
		return nil, nil, nil, entryScalar, false
	}
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
	if e.kind == entrySet {
		return errWrongType
	}
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
		if s.closing {
			if timer != nil {
				timer.Stop()
			}
			s.mu.Unlock()
			return nil, false
		}
		now := time.Now()
		e, exists := s.entryLocked(k, now)
		if !exists {
			if timer != nil {
				timer.Stop()
			}
			s.mu.Unlock()
			return nil, false
		}
		if e.kind == entryScalar || e.kind == entrySet {
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

func setMembersSorted(m map[string][]byte) [][]byte {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	out := make([][]byte, len(keys))
	for i, k := range keys {
		out[i] = append([]byte(nil), m[k]...)
	}
	return out
}

func sortStrings(ss []string) {
	sort.Strings(ss)
}

// SAdd returns 1 if member was added, 0 if already present.
func (s *cacheStore) SAdd(key, member []byte) (int, error) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkKeyLen(key); err != nil {
		return 0, err
	}
	if err := s.checkElem(member); err != nil {
		return 0, err
	}
	k := string(key)
	mk := string(member)
	e := s.data[k]
	if e != nil && e.expired(now) {
		s.deleteEntryLocked(k)
		e = nil
	}
	if e != nil && e.kind != entrySet {
		return 0, errWrongType
	}
	if e == nil {
		if err := s.enforceKeysCap(); err != nil {
			return 0, err
		}
		s.data[k] = &cacheEntry{
			kind:    entrySet,
			members: map[string][]byte{mk: append([]byte(nil), member...)},
			expiry:  nil,
		}
		return 1, nil
	}
	if _, ok := e.members[mk]; ok {
		return 0, nil
	}
	if len(e.members) >= maxCacheListLen {
		return 0, fmt.Errorf("%w: set size", errCacheLimit)
	}
	e.members[mk] = append([]byte(nil), member...)
	return 1, nil
}

// SRem returns 1 if member was removed, 0 if absent.
func (s *cacheStore) SRem(key, member []byte) (int, error) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkKeyLen(key); err != nil {
		return 0, err
	}
	k := string(key)
	e := s.data[k]
	if e == nil || e.expired(now) {
		if e != nil {
			s.deleteEntryLocked(k)
		}
		return 0, nil
	}
	if e.kind != entrySet {
		return 0, errWrongType
	}
	mk := string(member)
	if _, ok := e.members[mk]; !ok {
		return 0, nil
	}
	delete(e.members, mk)
	if len(e.members) == 0 {
		s.deleteEntryLocked(k)
	}
	return 1, nil
}

// SMembers returns sorted member copies; empty slice if key missing (Redis-aligned).
func (s *cacheStore) SMembers(key []byte) ([][]byte, error) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkKeyLen(key); err != nil {
		return nil, err
	}
	k := string(key)
	e, ok := s.entryLocked(k, now)
	if !ok {
		return [][]byte{}, nil
	}
	if e.kind != entrySet {
		return nil, errWrongType
	}
	return setMembersSorted(e.members), nil
}

// SetNX returns 1 if key was set, 0 if key already exists (any non-expired kind).
func (s *cacheStore) SetNX(key, value []byte, ttlSec int64) (int, error) {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkKeyLen(key); err != nil {
		return 0, err
	}
	if err := s.checkScalarVal(value); err != nil {
		return 0, err
	}
	k := string(key)
	if e, ok := s.entryLocked(k, now); ok && e != nil {
		return 0, nil
	}
	if err := s.enforceKeysCap(); err != nil {
		return 0, err
	}
	exp := wallExpiry(ttlSec)
	s.data[k] = &cacheEntry{kind: entryScalar, scalar: append([]byte(nil), value...), expiry: exp}
	s.broadcastKey(k)
	return 1, nil
}

// Keys returns up to limit key names matching prefix (sorted). Prefix must be non-empty.
func (s *cacheStore) Keys(prefix []byte, limit int) ([][]byte, error) {
	if len(prefix) == 0 {
		return nil, fmt.Errorf("%w: keys prefix required", errCacheLimit)
	}
	if limit <= 0 {
		limit = defaultCSKEYSLimit
	}
	if limit > maxCSKEYSLimit {
		limit = maxCSKEYSLimit
	}
	pfx := string(prefix)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()

	var matched []string
	for k, e := range s.data {
		if e.expired(now) {
			s.deleteEntryLocked(k)
			continue
		}
		if !strings.HasPrefix(k, pfx) {
			continue
		}
		matched = append(matched, k)
	}
	sortStrings(matched)
	if len(matched) > limit {
		matched = matched[:limit]
	}
	out := make([][]byte, len(matched))
	for i, k := range matched {
		out[i] = []byte(k)
	}
	return out, nil
}

// --- pub/sub (blocking one-shot receive) ---

type pubSubWaiter struct {
	msg       []byte
	ready     bool
	cancelled bool
}

type pubSubChannel struct {
	waiters []*pubSubWaiter
	cond    *sync.Cond
}

type pubSub struct {
	mu       sync.Mutex
	channels map[string]*pubSubChannel
	closing  bool
}

func newPubSub() *pubSub {
	ps := &pubSub{channels: make(map[string]*pubSubChannel)}
	return ps
}

func (ps *pubSub) channelLocked(name string) *pubSubChannel {
	ch, ok := ps.channels[name]
	if !ok {
		ch = &pubSubChannel{cond: sync.NewCond(&ps.mu)}
		ps.channels[name] = ch
	}
	return ch
}

func (ps *pubSub) removeWaiter(ch *pubSubChannel, w *pubSubWaiter) {
	for i, x := range ch.waiters {
		if x == w {
			ch.waiters = append(ch.waiters[:i], ch.waiters[i+1:]...)
			return
		}
	}
}

func (ps *pubSub) Subscribe(channel []byte, deadline time.Time) ([]byte, bool) {
	if err := checkPubSubName(channel); err != nil {
		return nil, false
	}
	name := string(channel)
	ps.mu.Lock()
	if ps.closing {
		ps.mu.Unlock()
		return nil, false
	}
	ch := ps.channelLocked(name)
	w := &pubSubWaiter{}
	ch.waiters = append(ch.waiters, w)

	timedOut := false
	timer := time.AfterFunc(time.Until(deadline), func() {
		ps.mu.Lock()
		timedOut = true
		ch.cond.Broadcast()
		ps.mu.Unlock()
	})

	for !w.ready && !w.cancelled && !ps.closing && !timedOut {
		ch.cond.Wait()
	}
	timer.Stop()

	var out []byte
	if w.ready {
		out = append([]byte(nil), w.msg...)
	}
	ps.removeWaiter(ch, w)
	if len(ch.waiters) == 0 {
		delete(ps.channels, name)
	}
	ps.mu.Unlock()
	if w.ready {
		return out, true
	}
	return nil, false
}

func (ps *pubSub) Publish(channel, message []byte) (int, error) {
	if err := checkPubSubName(channel); err != nil {
		return 0, err
	}
	if len(message) > maxCacheScalarLen {
		return 0, fmt.Errorf("%w: message size", errCacheLimit)
	}
	name := string(channel)
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.closing {
		return 0, nil
	}
	ch, ok := ps.channels[name]
	if !ok || len(ch.waiters) == 0 {
		return 0, nil
	}
	n := 0
	for _, w := range ch.waiters {
		if w.ready {
			continue
		}
		w.msg = append([]byte(nil), message...)
		w.ready = true
		n++
	}
	ch.cond.Broadcast()
	return n, nil
}

func (ps *pubSub) Shutdown() {
	ps.mu.Lock()
	ps.closing = true
	for _, ch := range ps.channels {
		for _, w := range ch.waiters {
			w.cancelled = true
		}
		ch.cond.Broadcast()
	}
	ps.mu.Unlock()
}

func checkPubSubName(channel []byte) error {
	if len(channel) == 0 || len(channel) > maxCacheKeyLen {
		return fmt.Errorf("%w: channel size", errCacheLimit)
	}
	return nil
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
	pubsub  *pubSub
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
		store:  newCacheStore(),
		pubsub: newPubSub(),
		ln:     ln,
		done:   make(chan struct{}),
		addr:   ln.Addr().String(),
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
		pubsub:  newPubSub(),
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
	s.store.Shutdown()
	s.pubsub.Shutdown()
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
			scalar, list, _, kind, ok := s.store.Get(parts[1])
			if !ok {
				_ = respWriteBulk(w, nil) // $-1
				continue
			}
			if kind == entrySet {
				_ = respWriteError(w, errWrongType.Error())
				continue
			}
			if kind == entryScalar {
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
				if errors.Is(err, errWrongType) {
					_ = respWriteError(w, errWrongType.Error())
				} else {
					_ = respWriteError(w, err.Error())
				}
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
		case "CSSADD":
			if len(parts) != 3 {
				_ = respWriteError(w, "wrong number of arguments for CSSADD")
				return
			}
			n, err := s.store.SAdd(parts[1], parts[2])
			if err != nil {
				if errors.Is(err, errWrongType) {
					_ = respWriteError(w, errWrongType.Error())
				} else {
					_ = respWriteError(w, err.Error())
				}
				continue
			}
			_ = respWriteInt(w, int64(n))
		case "CSSREM":
			if len(parts) != 3 {
				_ = respWriteError(w, "wrong number of arguments for CSSREM")
				return
			}
			n, err := s.store.SRem(parts[1], parts[2])
			if err != nil {
				if errors.Is(err, errWrongType) {
					_ = respWriteError(w, errWrongType.Error())
				} else {
					_ = respWriteError(w, err.Error())
				}
				continue
			}
			_ = respWriteInt(w, int64(n))
		case "CSSMEMBERS":
			if len(parts) != 2 {
				_ = respWriteError(w, "wrong number of arguments for CSSMEMBERS")
				return
			}
			members, err := s.store.SMembers(parts[1])
			if err != nil {
				if errors.Is(err, errWrongType) {
					_ = respWriteError(w, errWrongType.Error())
				} else {
					_ = respWriteError(w, err.Error())
				}
				continue
			}
			if len(members) == 0 {
				if err := respWriteArrayHeader(w, 0); err != nil {
					return
				}
				_ = w.Flush()
				continue
			}
			_ = respWriteArrayOfBulks(w, members)
		case "CSSETNX":
			if len(parts) != 3 && len(parts) != 4 {
				_ = respWriteError(w, "wrong number of arguments for CSSETNX")
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
			n, err := s.store.SetNX(parts[1], parts[2], ttl)
			if err != nil {
				_ = respWriteError(w, err.Error())
				continue
			}
			_ = respWriteInt(w, int64(n))
		case "CSKEYS":
			if len(parts) != 1 && len(parts) != 2 && len(parts) != 3 {
				_ = respWriteError(w, "wrong number of arguments for CSKEYS")
				return
			}
			prefix := []byte{}
			limit := defaultCSKEYSLimit
			if len(parts) >= 2 {
				prefix = parts[1]
			}
			if len(parts) == 3 {
				l, err := strconv.Atoi(string(parts[2]))
				if err != nil || l < 0 {
					_ = respWriteError(w, "invalid limit")
					return
				}
				limit = l
			}
			keys, err := s.store.Keys(prefix, limit)
			if err != nil {
				_ = respWriteError(w, err.Error())
				continue
			}
			if len(keys) == 0 {
				if err := respWriteArrayHeader(w, 0); err != nil {
					return
				}
				_ = w.Flush()
				continue
			}
			_ = respWriteArrayOfBulks(w, keys)
		case "CSPUBLISH":
			if len(parts) != 3 {
				_ = respWriteError(w, "wrong number of arguments for CSPUBLISH")
				return
			}
			n, err := s.pubsub.Publish(parts[1], parts[2])
			if err != nil {
				_ = respWriteError(w, err.Error())
				continue
			}
			_ = respWriteInt(w, int64(n))
		case "CSSUBSCRIBE":
			if len(parts) != 3 {
				_ = respWriteError(w, "wrong number of arguments for CSSUBSCRIBE")
				return
			}
			sec, err := strconv.ParseFloat(string(parts[2]), 64)
			if err != nil || math.IsNaN(sec) || math.IsInf(sec, 0) || sec <= 0 || sec > maxSubscribeTimeoutSec {
				_ = respWriteError(w, "invalid timeout")
				return
			}
			deadline := time.Now().Add(time.Duration(sec * float64(time.Second)))
			v, ok := s.pubsub.Subscribe(parts[1], deadline)
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
