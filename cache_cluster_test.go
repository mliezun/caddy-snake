package caddysnake

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testClusterSecret = "0123456789abcdef0123456789abcdef"

func startTestCacheCluster(t *testing.T, count int) []*cacheServer {
	t.Helper()
	listeners := make([]net.Listener, count)
	peers := make([]string, count)
	for i := range count {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listeners[i] = ln
		peers[i] = ln.Addr().String()
	}
	servers := make([]*cacheServer, count)
	for i := range count {
		srv, err := startCacheServerTCPOnly()
		if err != nil {
			t.Fatal(err)
		}
		cfg := &CacheConfig{
			Mode:      cacheModeCluster,
			Listen:    peers[i],
			Advertise: peers[i],
			Peers:     append([]string(nil), peers...),
			Namespace: "test-app",
			Secret:    testClusterSecret,
		}
		if err := srv.attachCluster(cfg, listeners[i]); err != nil {
			_ = srv.Close()
			t.Fatal(err)
		}
		servers[i] = srv
	}
	t.Cleanup(func() {
		for _, srv := range servers {
			if srv != nil {
				_ = srv.Close()
			}
		}
	})
	return servers
}

func keyOwnedBy(t *testing.T, cluster *clusterCache, owner string) []byte {
	t.Helper()
	for i := 0; i < 100_000; i++ {
		key := []byte("key-" + strconv.Itoa(i))
		if cluster.owner(key) == owner {
			return key
		}
	}
	t.Fatalf("could not find key owned by %s", owner)
	return nil
}

func TestClusterCacheRoutesScalarOperationsToOwner(t *testing.T) {
	servers := startTestCacheCluster(t, 3)
	client := servers[0].cluster
	remoteOwner := servers[1].cluster.self
	key := keyOwnedBy(t, client, remoteOwner)

	if err := client.Set(key, []byte("value"), 0); err != nil {
		t.Fatal(err)
	}
	value, ok, err := servers[2].cluster.Get(key)
	if err != nil || !ok || string(value) != "value" {
		t.Fatalf("Get() = %q, %v, %v", value, ok, err)
	}
	if _, _, _, _, ok := servers[0].store.Get(key); ok {
		t.Fatal("non-owner stored a fallback copy")
	}
	if value, _, _, kind, ok := servers[1].store.Get(key); !ok || kind != entryScalar || string(value) != "value" {
		t.Fatalf("owner value = %q, kind=%v, ok=%v", value, kind, ok)
	}

	deleted, err := servers[2].cluster.Delete(key)
	if err != nil || deleted != 1 {
		t.Fatalf("Delete() = %d, %v", deleted, err)
	}
	if _, ok, err := client.Get(key); err != nil || ok {
		t.Fatalf("Get() after delete: ok=%v err=%v", ok, err)
	}
}

func TestClusterCacheTTLExpiresOnOwner(t *testing.T) {
	servers := startTestCacheCluster(t, 2)
	client := servers[0].cluster
	key := keyOwnedBy(t, client, servers[1].cluster.self)
	if err := client.Set(key, []byte("short"), 1); err != nil {
		t.Fatal(err)
	}
	if value, ok, err := client.Get(key); err != nil || !ok || string(value) != "short" {
		t.Fatalf("initial Get() = %q, %v, %v", value, ok, err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, ok, err := client.Get(key); err != nil || ok {
		t.Fatalf("expired Get(): ok=%v err=%v", ok, err)
	}
}

func TestClusterCacheDoesNotFallbackWhenOwnerIsUnavailable(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unavailable := listener.Addr().String()
	_ = listener.Close()

	srv, err := startCacheServerTCPOnly()
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	peerLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &CacheConfig{
		Mode:      cacheModeCluster,
		Listen:    peerLn.Addr().String(),
		Advertise: peerLn.Addr().String(),
		Peers:     []string{unavailable},
		Namespace: "test-app",
		Secret:    testClusterSecret,
	}
	if err := srv.attachCluster(cfg, peerLn); err != nil {
		t.Fatal(err)
	}
	key := keyOwnedBy(t, srv.cluster, unavailable)
	if err := srv.cluster.Set(key, []byte("value"), 0); err == nil {
		t.Fatal("expected unavailable owner error")
	}
	if _, _, _, _, ok := srv.store.Get(key); ok {
		t.Fatal("failed remote write must not be stored locally")
	}
}

func TestClusterPeerRejectsWrongSecretAndNamespace(t *testing.T) {
	servers := startTestCacheCluster(t, 1)
	for _, auth := range [][][]byte{
		{[]byte(clusterPeerAuthCommand), []byte("test-app"), []byte(servers[0].cluster.ringID), []byte("wrong-secret-wrong-secret")},
		{[]byte(clusterPeerAuthCommand), []byte("wrong-app"), []byte(servers[0].cluster.ringID), []byte(testClusterSecret)},
		{[]byte(clusterPeerAuthCommand), []byte("test-app"), []byte("wrong-ring"), []byte(testClusterSecret)},
	} {
		conn, err := net.Dial("tcp", servers[0].cluster.self)
		if err != nil {
			t.Fatal(err)
		}
		w := bufio.NewWriter(conn)
		if err := respWriteCommand(w, auth); err != nil {
			t.Fatal(err)
		}
		line, err := respReadLine(bufio.NewReader(conn))
		_ = conn.Close()
		if err != nil || !strings.Contains(string(line), "NOAUTH") {
			t.Fatalf("auth reply = %q, %v", line, err)
		}
	}
}

func TestClusterPeerRejectsKeyOwnedByAnotherPeer(t *testing.T) {
	servers := startTestCacheCluster(t, 2)
	key := keyOwnedBy(t, servers[0].cluster, servers[1].cluster.self)
	conn, err := net.Dial("tcp", servers[0].cluster.self)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	if err := respWriteCommand(w, [][]byte{
		[]byte(clusterPeerAuthCommand),
		[]byte(servers[0].cluster.namespace),
		[]byte(servers[0].cluster.ringID),
		[]byte(servers[0].cluster.secret),
	}); err != nil {
		t.Fatal(err)
	}
	if reply, err := readClusterReply(r); err != nil || reply.kind != '+' {
		t.Fatalf("auth reply = %#v, %v", reply, err)
	}
	if err := respWriteCommand(w, [][]byte{[]byte("CSSET"), key, []byte("wrong-owner")}); err != nil {
		t.Fatal(err)
	}
	line, err := respReadLine(r)
	if err != nil || !strings.Contains(string(line), "not owner") {
		t.Fatalf("CSSET reply = %q, %v", line, err)
	}
}

func TestClusterWorkerProtocolRejectsNonScalarCommands(t *testing.T) {
	servers := startTestCacheCluster(t, 1)
	conn := dialCacheServer(t, servers[0])
	defer conn.Close()
	r := bufio.NewReader(conn)
	if err := respWriteCommand(bufio.NewWriter(conn), [][]byte{[]byte("CSAPPEND"), []byte("q"), []byte("v")}); err != nil {
		t.Fatal(err)
	}
	line, err := respReadLine(r)
	if err != nil || !strings.Contains(string(line), clusterUnsupportedError) {
		t.Fatalf("reply = %q, %v", line, err)
	}
}

func TestClusterWorkerProtocolRoutesScalarCommands(t *testing.T) {
	servers := startTestCacheCluster(t, 3)
	key := keyOwnedBy(t, servers[0].cluster, servers[1].cluster.self)

	writerConn := dialCacheServer(t, servers[0])
	defer writerConn.Close()
	writerReader := bufio.NewReader(writerConn)
	if err := respWriteCommand(bufio.NewWriter(writerConn), [][]byte{[]byte("CSSET"), key, []byte("from-worker")}); err != nil {
		t.Fatal(err)
	}
	if reply, err := readClusterReply(writerReader); err != nil || reply.kind != '+' || reply.text != "OK" {
		t.Fatalf("CSSET reply = %#v, %v", reply, err)
	}

	readerConn := dialCacheServer(t, servers[2])
	defer readerConn.Close()
	reader := bufio.NewReader(readerConn)
	if err := respWriteCommand(bufio.NewWriter(readerConn), [][]byte{[]byte("CSGET"), key}); err != nil {
		t.Fatal(err)
	}
	if reply, err := readClusterReply(reader); err != nil || reply.kind != '$' || string(reply.bulk) != "from-worker" {
		t.Fatalf("CSGET reply = %#v, %v", reply, err)
	}
}

func TestClusterOwnershipIsIndependentOfPeerOrder(t *testing.T) {
	store := newCacheStore()
	base := &CacheConfig{
		Mode:      cacheModeCluster,
		Listen:    "127.0.0.1:7001",
		Advertise: "node-a:7001",
		Peers:     []string{"node-c:7001", "node-b:7001"},
		Namespace: "app",
		Secret:    testClusterSecret,
	}
	a, err := newClusterCache(store, base)
	if err != nil {
		t.Fatal(err)
	}
	reversed := cloneCacheConfig(base)
	reversed.Peers = []string{"node-b:7001", "node-a:7001", "node-c:7001"}
	b, err := newClusterCache(store, reversed)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range [][]byte{[]byte("a"), []byte("b"), []byte("customer:42")} {
		if a.owner(key) != b.owner(key) {
			t.Fatalf("owner(%q): %q != %q", key, a.owner(key), b.owner(key))
		}
	}
}

func TestCacheConfigValidationAndCaddyfile(t *testing.T) {
	input := `python {
		module_asgi main:app
		cache cluster {
			listen :7447
			advertise node-a:7447
			peers node-a:7447 node-b:7447
			namespace my-app
			secret 0123456789abcdef
		}
	}`
	f := loadPythonHandlerFromCaddyfile(t, input)
	if f.Cache == nil || f.Cache.effectiveMode() != cacheModeCluster {
		t.Fatalf("Cache = %#v", f.Cache)
	}
	if len(f.Cache.Peers) != 2 || f.Cache.Namespace != "my-app" {
		t.Fatalf("Cache = %#v", f.Cache)
	}
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}

	f.Cache.Secret = "short"
	if err := f.Validate(); err == nil || !strings.Contains(err.Error(), "between 16 and 4096") {
		t.Fatalf("Validate() = %v", err)
	}
	f.Cache.Secret = testClusterSecret
	f.Cache.Peers = []string{""}
	if err := f.Validate(); err == nil || !strings.Contains(err.Error(), "address is required") {
		t.Fatalf("Validate() = %v", err)
	}
}

func TestBuildCacheConfigFromCLI(t *testing.T) {
	cfg, err := buildCacheConfigFromCLI("cluster", ":7447", "node-a:7447", "app", testClusterSecret, []string{"node-b:7447"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg == nil || cfg.Mode != cacheModeCluster || len(cfg.Peers) != 1 {
		t.Fatalf("cfg = %#v", cfg)
	}
	if _, err := buildCacheConfigFromCLI("cluster", "", "", "", "", nil); err == nil {
		t.Fatal("expected incomplete cluster config error")
	}
}
