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
	sc, list, isList, ok := s.Get([]byte("k"))
	if !ok || isList || string(sc) != "v" || list != nil {
		t.Fatalf("unexpected get: sc=%q list=%v isList=%v ok=%v", sc, list, isList, ok)
	}
}

func TestCacheStore_SetReplacesList(t *testing.T) {
	s := newCacheStore()
	_ = s.Append([]byte("k"), []byte("a"))
	if err := s.Set([]byte("k"), []byte("x"), 0); err != nil {
		t.Fatal(err)
	}
	sc, _, isList, ok := s.Get([]byte("k"))
	if !ok || isList || string(sc) != "x" {
		t.Fatalf("expected scalar x, got ok=%v isList=%v sc=%q", ok, isList, sc)
	}
}

func TestCacheStore_EmptyListGet(t *testing.T) {
	s := newCacheStore()
	_ = s.Append([]byte("k"), []byte("a"))
	_, _ = s.Pop([]byte("k"), nil)
	_, list, isList, ok := s.Get([]byte("k"))
	if !ok || !isList || len(list) != 0 {
		t.Fatalf("expected empty list, ok=%v isList=%v len=%d", ok, isList, len(list))
	}
}

func TestCacheStore_AppendClearsTTL(t *testing.T) {
	s := newCacheStore()
	_ = s.Set([]byte("k"), []byte("v"), 3600)
	_ = s.Append([]byte("k"), []byte("x"))
	_, _, isList, ok := s.Get([]byte("k"))
	if !ok || !isList {
		t.Fatalf("expected list after append promote, ok=%v isList=%v", ok, isList)
	}
	// entry should not be expired immediately
	_, _, _, ok2 := s.Get([]byte("k"))
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
	sc, _, isList, ok := s.Get([]byte("k"))
	if !ok || isList || string(sc) != "scalar" {
		t.Fatalf("expected scalar preserved, ok=%v isList=%v sc=%q", ok, isList, sc)
	}
}

func TestCacheTCPServer_CRUD(t *testing.T) {
	srv, err := startCacheTCPServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
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

func TestCacheTCPServer_AppendAndPop(t *testing.T) {
	srv, err := startCacheTCPServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
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

func TestCacheTCPServer_EmptyListCSGET(t *testing.T) {
	srv, err := startCacheTCPServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
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

func TestCacheTCPServer_CSPOPTimeout(t *testing.T) {
	srv, err := startCacheTCPServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
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

func TestCacheTCPServer_CSQUIT(t *testing.T) {
	srv, err := startCacheTCPServer()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
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
