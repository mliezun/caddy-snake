package caddysnake

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func respStringArrayCmd(parts ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(parts))
	for _, p := range parts {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(p), p)
	}
	return b.String()
}

func readProtoLine(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read line: %v", err)
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		t.Fatalf("bad line ending: %q", line)
	}
	return string(line[:len(line)-2])
}

func readBulkString(t *testing.T, r *bufio.Reader) []byte {
	t.Helper()
	line := readProtoLine(t, r)
	if !strings.HasPrefix(line, "$") {
		t.Fatalf("expected bulk, got %q", line)
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil {
		t.Fatalf("bulk len: %v", err)
	}
	if n == -1 {
		return nil
	}
	buf := make([]byte, n+2)
	if _, err := io.ReadFull(r, buf); err != nil {
		t.Fatalf("read bulk body: %v", err)
	}
	if buf[n] != '\r' || buf[n+1] != '\n' {
		t.Fatalf("bulk trailer")
	}
	return buf[:n]
}

func TestCacheStore_SetGetScalar(t *testing.T) {
	s := newCacheStore()
	if err := s.Set([]byte("k"), []byte("v"), 0); err != nil {
		t.Fatal(err)
	}
	sc, list, _, kind, ok := s.Get([]byte("k"))
	if !ok || kind != entryScalar || string(sc) != "v" || list != nil {
		t.Fatalf("unexpected get: sc=%q list=%v kind=%v ok=%v", sc, list, kind, ok)
	}
}

func TestCacheStore_SetReplacesList(t *testing.T) {
	s := newCacheStore()
	_ = s.Append([]byte("k"), []byte("a"))
	if err := s.Set([]byte("k"), []byte("x"), 0); err != nil {
		t.Fatal(err)
	}
	sc, _, _, kind, ok := s.Get([]byte("k"))
	if !ok || kind != entryScalar || string(sc) != "x" {
		t.Fatalf("expected scalar x, got ok=%v kind=%v sc=%q", ok, kind, sc)
	}
}

func TestCacheStore_EmptyListGet(t *testing.T) {
	s := newCacheStore()
	_ = s.Append([]byte("k"), []byte("a"))
	_, _ = s.Pop([]byte("k"), nil)
	_, list, _, kind, ok := s.Get([]byte("k"))
	if !ok || kind != entryList || len(list) != 0 {
		t.Fatalf("expected empty list, ok=%v kind=%v len=%d", ok, kind, len(list))
	}
}

func TestCacheStore_AppendClearsTTL(t *testing.T) {
	s := newCacheStore()
	_ = s.Set([]byte("k"), []byte("v"), 3600)
	_ = s.Append([]byte("k"), []byte("x"))
	_, _, _, kind, ok := s.Get([]byte("k"))
	if !ok || kind != entryList {
		t.Fatalf("expected list after append promote, ok=%v kind=%v", ok, kind)
	}
	// entry should not be expired immediately
	_, _, _, _, ok2 := s.Get([]byte("k"))
	if !ok2 {
		t.Fatal("expected key still present")
	}
}

func TestCacheStore_DeleteCount(t *testing.T) {
	s := newCacheStore()
	if n := s.Delete([]byte("k")); n != 0 {
		t.Fatalf("del missing: %d", n)
	}
	_ = s.Set([]byte("k"), []byte("v"), 0)
	if n := s.Delete([]byte("k")); n != 1 {
		t.Fatalf("del existing: %d", n)
	}
}

func TestCacheStore_PopNonBlockingNil(t *testing.T) {
	s := newCacheStore()
	v, ok := s.Pop([]byte("missing"), nil)
	if ok || v != nil {
		t.Fatalf("pop missing: ok=%v v=%q", ok, v)
	}
	_ = s.Set([]byte("k"), []byte("s"), 0)
	v, ok = s.Pop([]byte("k"), nil)
	if ok || v != nil {
		t.Fatalf("pop scalar: ok=%v v=%q", ok, v)
	}
}

func TestCacheStore_PopBlocksUntilAppend(t *testing.T) {
	s := newCacheStore()
	_ = s.Append([]byte("k"), []byte("first"))
	_, _ = s.Pop([]byte("k"), nil)

	var wg sync.WaitGroup
	wg.Add(1)
	var got []byte
	var popOK bool
	go func() {
		defer wg.Done()
		got, popOK = s.Pop([]byte("k"), nil)
	}()

	time.Sleep(30 * time.Millisecond)
	_ = s.Append([]byte("k"), []byte("second"))

	wg.Wait()
	if !popOK || string(got) != "second" {
		t.Fatalf("expected second, ok=%v got=%q", popOK, got)
	}
}

func TestCacheStore_DeleteUnblocksPop(t *testing.T) {
	s := newCacheStore()
	_ = s.Append([]byte("k"), []byte("a"))
	_, _ = s.Pop([]byte("k"), nil)

	var wg sync.WaitGroup
	wg.Add(1)
	var popOK bool
	go func() {
		defer wg.Done()
		_, popOK = s.Pop([]byte("k"), nil)
	}()

	time.Sleep(20 * time.Millisecond)
	_ = s.Delete([]byte("k"))

	wg.Wait()
	if popOK {
		t.Fatal("expected immediate nil pop after delete")
	}
}

func TestCacheStore_SetScalarUnblocksWaitingPop(t *testing.T) {
	s := newCacheStore()
	_ = s.Append([]byte("k"), []byte("a"))
	_, _ = s.Pop([]byte("k"), nil)

	var wg sync.WaitGroup
	wg.Add(1)
	var popOK bool
	go func() {
		defer wg.Done()
		_, popOK = s.Pop([]byte("k"), nil)
	}()

	time.Sleep(20 * time.Millisecond)
	_ = s.Set([]byte("k"), []byte("scalar"), 0)

	wg.Wait()
	if popOK {
		t.Fatal("expected pop to wake with nil when key becomes scalar")
	}
	sc, _, _, kind, ok := s.Get([]byte("k"))
	if !ok || kind != entryScalar || string(sc) != "scalar" {
		t.Fatalf("expected scalar preserved, ok=%v kind=%v sc=%q", ok, kind, sc)
	}
}

func dialCacheServer(t *testing.T, srv *cacheServer) net.Conn {
	t.Helper()
	addr := srv.Addr()
	if strings.HasPrefix(addr, "unix://") {
		path := strings.TrimPrefix(addr, "unix://")
		c, err := net.Dial("unix", path)
		if err != nil {
			t.Fatalf("dial unix: %v", err)
		}
		return c
	}
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial tcp: %v", err)
	}
	return c
}

func TestCacheServer_CRUD(t *testing.T) {
	srv, err := startCacheServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	conn := dialCacheServer(t, srv)
	defer conn.Close()
	r := bufio.NewReader(conn)

	_, _ = io.WriteString(conn, respStringArrayCmd("CSGET", "foo"))
	line := readProtoLine(t, r)
	if line != "$-1" {
		t.Fatalf("CSGET miss want $-1, got %q", line)
	}

	_, _ = io.WriteString(conn, respStringArrayCmd("CSSET", "foo", "bar"))
	line = readProtoLine(t, r)
	if line != "+OK" {
		t.Fatalf("CSSET want +OK, got %q", line)
	}

	_, _ = io.WriteString(conn, respStringArrayCmd("CSGET", "foo"))
	b := readBulkString(t, r)
	if string(b) != "bar" {
		t.Fatalf("CSGET want bar, got %q", b)
	}

	_, _ = io.WriteString(conn, respStringArrayCmd("CSDEL", "foo"))
	line = readProtoLine(t, r)
	if line != ":1" {
		t.Fatalf("CSDEL want :1, got %q", line)
	}
}

func TestCacheServer_AppendAndPop(t *testing.T) {
	srv, err := startCacheServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	conn := dialCacheServer(t, srv)
	defer conn.Close()
	r := bufio.NewReader(conn)

	_, _ = io.WriteString(conn, respStringArrayCmd("CSAPPEND", "q", "a"))
	if readProtoLine(t, r) != "+OK" {
		t.Fatal("append")
	}
	_, _ = io.WriteString(conn, respStringArrayCmd("CSAPPEND", "q", "b"))
	if readProtoLine(t, r) != "+OK" {
		t.Fatal("append2")
	}

	_, _ = io.WriteString(conn, respStringArrayCmd("CSGET", "q"))
	line := readProtoLine(t, r)
	if line != "*2" {
		t.Fatalf("want *2, got %q", line)
	}
	if string(readBulkString(t, r)) != "a" {
		t.Fatal("elem0")
	}
	if string(readBulkString(t, r)) != "b" {
		t.Fatal("elem1")
	}

	_, _ = io.WriteString(conn, respStringArrayCmd("CSPOP", "q"))
	if string(readBulkString(t, r)) != "a" {
		t.Fatal("pop")
	}

	_, _ = io.WriteString(conn, respStringArrayCmd("CSGET", "q"))
	line = readProtoLine(t, r)
	if line != "*1" {
		t.Fatalf("want *1, got %q", line)
	}
	if string(readBulkString(t, r)) != "b" {
		t.Fatal("get after pop")
	}
}

func TestCacheServer_EmptyListCSGET(t *testing.T) {
	srv, err := startCacheServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	conn := dialCacheServer(t, srv)
	defer conn.Close()
	r := bufio.NewReader(conn)

	_, _ = io.WriteString(conn, respStringArrayCmd("CSAPPEND", "e", "x"))
	readProtoLine(t, r)
	_, _ = io.WriteString(conn, respStringArrayCmd("CSPOP", "e"))
	readBulkString(t, r)

	_, _ = io.WriteString(conn, respStringArrayCmd("CSGET", "e"))
	line := readProtoLine(t, r)
	if line != "*0" {
		t.Fatalf("empty list CSGET want *0, got %q", line)
	}
}

func TestCacheServer_CSPOPTimeout(t *testing.T) {
	srv, err := startCacheServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	conn := dialCacheServer(t, srv)
	defer conn.Close()
	r := bufio.NewReader(conn)

	_, _ = io.WriteString(conn, respStringArrayCmd("CSAPPEND", "t", "only"))
	readProtoLine(t, r)
	_, _ = io.WriteString(conn, respStringArrayCmd("CSPOP", "t"))
	readBulkString(t, r)

	_, _ = io.WriteString(conn, respStringArrayCmd("CSPOP", "t", "0.05"))
	line := readProtoLine(t, r)
	if line != "$-1" {
		t.Fatalf("timeout pop want $-1, got %q", line)
	}
}

func TestCacheServer_CSQUIT(t *testing.T) {
	srv, err := startCacheServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	conn := dialCacheServer(t, srv)
	defer conn.Close()
	r := bufio.NewReader(conn)

	_, _ = io.WriteString(conn, respStringArrayCmd("CSQUIT"))
	if readProtoLine(t, r) != "+OK" {
		t.Fatal("quit")
	}
	if _, err := r.ReadByte(); err != io.EOF {
		t.Fatalf("expected EOF after quit, got %v", err)
	}
}

func TestCacheStore_SetAddMembersUnique(t *testing.T) {
	s := newCacheStore()
	n, err := s.SAdd([]byte("g"), []byte("a"))
	if err != nil || n != 1 {
		t.Fatalf("sadd: n=%d err=%v", n, err)
	}
	n, err = s.SAdd([]byte("g"), []byte("a"))
	if err != nil || n != 0 {
		t.Fatalf("sadd dup: n=%d err=%v", n, err)
	}
	members, err := s.SMembers([]byte("g"))
	if err != nil || len(members) != 1 || string(members[0]) != "a" {
		t.Fatalf("smembers: %v err=%v", members, err)
	}
}

func TestCacheStore_SetRemoveCountsAndEmptySet(t *testing.T) {
	s := newCacheStore()
	_, _ = s.SAdd([]byte("g"), []byte("a"))
	n, err := s.SRem([]byte("g"), []byte("a"))
	if err != nil || n != 1 {
		t.Fatalf("srem: n=%d err=%v", n, err)
	}
	n, err = s.SRem([]byte("g"), []byte("a"))
	if err != nil || n != 0 {
		t.Fatalf("srem missing: n=%d err=%v", n, err)
	}
	members, err := s.SMembers([]byte("g"))
	if err != nil || len(members) != 0 {
		t.Fatalf("expected empty set, got %v", members)
	}
}

func TestCacheStore_SetTypeReplacementRules(t *testing.T) {
	s := newCacheStore()
	_, _ = s.SAdd([]byte("k"), []byte("m"))
	if err := s.Set([]byte("k"), []byte("scalar"), 0); err != nil {
		t.Fatal(err)
	}
	_, err := s.SAdd([]byte("k"), []byte("x"))
	if err != errWrongType {
		t.Fatalf("sadd on scalar: %v", err)
	}
	_ = s.Append([]byte("l"), []byte("a"))
	_, err = s.SAdd([]byte("l"), []byte("x"))
	if err != errWrongType {
		t.Fatalf("sadd on list: %v", err)
	}
	_, _ = s.SAdd([]byte("s"), []byte("m"))
	if err := s.Append([]byte("s"), []byte("a")); err != errWrongType {
		t.Fatalf("append on set: %v", err)
	}
}

func TestCacheStore_SetNXConcurrentSingleWinner(t *testing.T) {
	s := newCacheStore()
	var wg sync.WaitGroup
	wins := make([]int, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			n, err := s.SetNX([]byte("lock"), []byte("v"), 0)
			if err != nil {
				t.Errorf("setnx: %v", err)
				return
			}
			if n == 1 {
				wins[idx] = 1
			}
		}(i)
	}
	wg.Wait()
	total := 0
	for _, w := range wins {
		total += w
	}
	if total != 1 {
		t.Fatalf("expected 1 winner, got %d", total)
	}
}

func TestCacheStore_SetNXExpiredKeyCanBeReclaimed(t *testing.T) {
	s := newCacheStore()
	n, err := s.SetNX([]byte("k"), []byte("a"), 1)
	if err != nil || n != 1 {
		t.Fatalf("setnx: n=%d err=%v", n, err)
	}
	time.Sleep(1100 * time.Millisecond)
	n, err = s.SetNX([]byte("k"), []byte("b"), 0)
	if err != nil || n != 1 {
		t.Fatalf("reclaim: n=%d err=%v", n, err)
	}
	sc, _, _, kind, ok := s.Get([]byte("k"))
	if !ok || kind != entryScalar || string(sc) != "b" {
		t.Fatalf("get after reclaim: ok=%v sc=%q", ok, sc)
	}
}

func TestCacheServer_CSSADD_CSSMEMBERS_RESP(t *testing.T) {
	srv, err := startCacheServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	conn := dialCacheServer(t, srv)
	defer conn.Close()
	r := bufio.NewReader(conn)

	_, _ = io.WriteString(conn, respStringArrayCmd("CSSADD", "g", "a"))
	if readProtoLine(t, r) != ":1" {
		t.Fatal("sadd")
	}
	_, _ = io.WriteString(conn, respStringArrayCmd("CSSMEMBERS", "g"))
	if readProtoLine(t, r) != "*1" {
		t.Fatal("smembers header")
	}
	if string(readBulkString(t, r)) != "a" {
		t.Fatal("member")
	}
	_, _ = io.WriteString(conn, respStringArrayCmd("CSSMEMBERS", "missing"))
	if readProtoLine(t, r) != "*0" {
		t.Fatal("smembers missing")
	}
}

func TestCacheServer_CSSETNXCreatesAndRejectsExisting(t *testing.T) {
	srv, err := startCacheServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	conn := dialCacheServer(t, srv)
	defer conn.Close()
	r := bufio.NewReader(conn)

	_, _ = io.WriteString(conn, respStringArrayCmd("CSSETNX", "k", "v"))
	if readProtoLine(t, r) != ":1" {
		t.Fatal("setnx create")
	}
	_, _ = io.WriteString(conn, respStringArrayCmd("CSSETNX", "k", "other"))
	if readProtoLine(t, r) != ":0" {
		t.Fatal("setnx reject")
	}
}

func TestCacheServer_CSKEYSPrefixAndAllTypes(t *testing.T) {
	srv, err := startCacheServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	conn := dialCacheServer(t, srv)
	defer conn.Close()
	r := bufio.NewReader(conn)

	_, _ = io.WriteString(conn, respStringArrayCmd("CSSET", "app:scalar", "v"))
	readProtoLine(t, r)
	_, _ = io.WriteString(conn, respStringArrayCmd("CSAPPEND", "app:list", "a"))
	readProtoLine(t, r)
	_, _ = io.WriteString(conn, respStringArrayCmd("CSSADD", "app:set", "m"))
	readProtoLine(t, r)
	_, _ = io.WriteString(conn, respStringArrayCmd("CSSET", "other", "x"))
	readProtoLine(t, r)

	_, _ = io.WriteString(conn, respStringArrayCmd("CSKEYS", "app:", "100"))
	line := readProtoLine(t, r)
	if line != "*3" {
		t.Fatalf("keys want *3, got %q", line)
	}
}

func TestCacheServer_PubSubPublishUnblocksSubscribers(t *testing.T) {
	srv, err := startCacheServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	var wg sync.WaitGroup
	var got []byte
	wg.Add(1)
	go func() {
		defer wg.Done()
		c := dialCacheServer(t, srv)
		defer c.Close()
		r := bufio.NewReader(c)
		_, _ = io.WriteString(c, respStringArrayCmd("CSSUBSCRIBE", "ch", "5"))
		got = readBulkString(t, r)
	}()

	time.Sleep(50 * time.Millisecond)
	pub := dialCacheServer(t, srv)
	defer pub.Close()
	pr := bufio.NewReader(pub)
	_, _ = io.WriteString(pub, respStringArrayCmd("CSPUBLISH", "ch", "hello"))
	if readProtoLine(t, pr) != ":1" {
		t.Fatal("publish count")
	}

	wg.Wait()
	if string(got) != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestCacheServer_PubSubMultipleSubscribersAndPublishCount(t *testing.T) {
	srv, err := startCacheServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	startSub := func(out *[]byte) *sync.WaitGroup {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := dialCacheServer(t, srv)
			defer c.Close()
			r := bufio.NewReader(c)
			_, _ = io.WriteString(c, respStringArrayCmd("CSSUBSCRIBE", "fan", "5"))
			*out = readBulkString(t, r)
		}()
		return &wg
	}
	var a, b []byte
	wg1 := startSub(&a)
	wg2 := startSub(&b)
	time.Sleep(50 * time.Millisecond)

	pub := dialCacheServer(t, srv)
	defer pub.Close()
	pr := bufio.NewReader(pub)
	_, _ = io.WriteString(pub, respStringArrayCmd("CSPUBLISH", "fan", "msg"))
	if readProtoLine(t, pr) != ":2" {
		t.Fatal("publish count")
	}
	wg1.Wait()
	wg2.Wait()
	if string(a) != "msg" || string(b) != "msg" {
		t.Fatalf("a=%q b=%q", a, b)
	}
}

func TestCacheServer_CloseUnblocksSubscribersAndPops(t *testing.T) {
	srv, err := startCacheServer()
	if err != nil {
		t.Fatal(err)
	}
	_ = srv.store.Append([]byte("q"), []byte("only"))
	_, _ = srv.store.Pop([]byte("q"), nil)

	popDone := make(chan struct{})
	go func() {
		defer close(popDone)
		_, _ = srv.store.Pop([]byte("q"), nil)
	}()

	subDone := make(chan struct{})
	go func() {
		defer close(subDone)
		_, _ = srv.pubsub.Subscribe([]byte("c"), time.Now().Add(30*time.Second))
	}()

	time.Sleep(30 * time.Millisecond)
	_ = srv.Close()

	select {
	case <-popDone:
	case <-time.After(2 * time.Second):
		t.Fatal("pop not unblocked")
	}
	select {
	case <-subDone:
	case <-time.After(2 * time.Second):
		t.Fatal("subscribe not unblocked")
	}
}

func TestCacheServer_CSKEYSRejectsEmptyPrefix(t *testing.T) {
	srv, err := startCacheServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	conn := dialCacheServer(t, srv)
	defer conn.Close()
	r := bufio.NewReader(conn)

	_, _ = io.WriteString(conn, respStringArrayCmd("CSKEYS", "", "10"))
	line := readProtoLine(t, r)
	if !strings.HasPrefix(line, "-ERR") {
		t.Fatalf("want ERR for empty prefix, got %q", line)
	}
}

func TestCacheStore_SRemDeletesEmptySet(t *testing.T) {
	s := newCacheStore()
	_, _ = s.SAdd([]byte("g"), []byte("only"))
	_, _ = s.SRem([]byte("g"), []byte("only"))
	_, _, _, _, ok := s.Get([]byte("g"))
	if ok {
		t.Fatal("empty set should remove key")
	}
}

func TestCacheServer_CSGETWrongTypeOnSet(t *testing.T) {
	srv, err := startCacheServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	conn := dialCacheServer(t, srv)
	defer conn.Close()
	r := bufio.NewReader(conn)

	_, _ = io.WriteString(conn, respStringArrayCmd("CSSADD", "s", "m"))
	readProtoLine(t, r)
	_, _ = io.WriteString(conn, respStringArrayCmd("CSGET", "s"))
	line := readProtoLine(t, r)
	if !strings.HasPrefix(line, "-ERR") || !strings.Contains(line, "wrong type") {
		t.Fatalf("want wrong type err, got %q", line)
	}
}
