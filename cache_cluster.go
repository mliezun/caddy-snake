package caddysnake

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

const (
	cacheModeLocal          = "local"
	cacheModeCluster        = "cluster"
	clusterPeerTimeout      = 3 * time.Second
	maxClusterPeers         = 128
	maxClusterNamespaceLen  = 256
	minClusterSecretBytes   = 16
	maxClusterSecretBytes   = 4096
	clusterPeerAuthCommand  = "CSPEERAUTH"
	clusterUnsupportedError = "cluster cache supports only CSGET, CSSET, and CSDEL"
)

// CacheConfig selects the cache topology. The zero value keeps the existing
// process-local cache. Cluster mode uses a static peer set and deterministic
// rendezvous hashing; one peer is authoritative for each scalar key.
type CacheConfig struct {
	// Mode is "local" (default) or "cluster".
	Mode string `json:"mode,omitempty"`
	// Listen is the TCP host:port for inbound cluster peer RPC.
	Listen string `json:"listen,omitempty"`
	// Advertise is this node's dialable host:port and stable hash-ring identity.
	Advertise string `json:"advertise,omitempty"`
	// Peers is the static cluster peer set; Advertise is added automatically.
	Peers []string `json:"peers,omitempty"`
	// Namespace separates independent clusters and participates in key ownership.
	Namespace string `json:"namespace,omitempty"`
	// Secret authenticates peer RPC and must contain 16 to 4096 bytes.
	Secret string `json:"secret,omitempty"`
}

func (c *CacheConfig) effectiveMode() string {
	if c == nil || strings.TrimSpace(c.Mode) == "" {
		return cacheModeLocal
	}
	return strings.ToLower(strings.TrimSpace(c.Mode))
}

func (c *CacheConfig) validate() error {
	if c == nil {
		return nil
	}
	switch c.effectiveMode() {
	case cacheModeLocal:
		if c.Listen != "" || c.Advertise != "" || len(c.Peers) > 0 || c.Namespace != "" || c.Secret != "" {
			return errors.New("cache local does not accept cluster options")
		}
		return nil
	case cacheModeCluster:
		if strings.TrimSpace(c.Listen) == "" {
			return errors.New("cache cluster requires listen")
		}
		if _, err := validateClusterListenAddress(c.Listen); err != nil {
			return fmt.Errorf("cache cluster listen: %w", err)
		}
		advertise, err := canonicalClusterPeer(c.Advertise)
		if err != nil {
			return fmt.Errorf("cache cluster advertise: %w", err)
		}
		if advertise == "" {
			return errors.New("cache cluster requires advertise")
		}
		ns := strings.TrimSpace(c.Namespace)
		if ns == "" {
			return errors.New("cache cluster requires namespace")
		}
		if len(ns) > maxClusterNamespaceLen || strings.ContainsAny(ns, "\r\n") {
			return fmt.Errorf("cache cluster namespace must be at most %d bytes and contain no newlines", maxClusterNamespaceLen)
		}
		if len(c.Secret) < minClusterSecretBytes || len(c.Secret) > maxClusterSecretBytes {
			return fmt.Errorf(
				"cache cluster secret must be between %d and %d bytes",
				minClusterSecretBytes,
				maxClusterSecretBytes,
			)
		}
		uniquePeers := map[string]struct{}{advertise: {}}
		for i, peer := range c.Peers {
			canonical, err := canonicalClusterPeer(peer)
			if err != nil {
				return fmt.Errorf("cache cluster peer %d: %w", i, err)
			}
			if canonical == "" {
				return fmt.Errorf("cache cluster peer %d: address is required", i)
			}
			uniquePeers[canonical] = struct{}{}
		}
		if len(uniquePeers) > maxClusterPeers {
			return fmt.Errorf("cache cluster supports at most %d peers", maxClusterPeers)
		}
		return nil
	default:
		return fmt.Errorf("unknown cache mode %q", c.Mode)
	}
}

func validateClusterListenAddress(address string) (string, error) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil {
		return "", fmt.Errorf("expected host:port: %w", err)
	}
	if port == "" {
		return "", errors.New("port is required")
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return "", errors.New("port must be between 1 and 65535")
	}
	return net.JoinHostPort(host, port), nil
}

func canonicalClusterPeer(address string) (string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", nil
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("expected host:port: %w", err)
	}
	host = strings.TrimSpace(host)
	ip := net.ParseIP(host)
	if host == "" || (ip != nil && ip.IsUnspecified()) {
		return "", errors.New("host must be a dialable hostname or IP address")
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil || n == 0 {
		return "", errors.New("port must be between 1 and 65535")
	}
	if ip != nil {
		host = ip.String()
	} else {
		host = strings.ToLower(host)
	}
	return net.JoinHostPort(host, port), nil
}

func normalizeClusterConfig(src *CacheConfig) (*CacheConfig, error) {
	if src == nil {
		return nil, nil
	}
	if err := src.validate(); err != nil {
		return nil, err
	}
	if src.effectiveMode() == cacheModeLocal {
		return nil, nil
	}
	self, _ := canonicalClusterPeer(src.Advertise)
	seen := map[string]struct{}{self: {}}
	peers := []string{self}
	for _, raw := range src.Peers {
		peer, _ := canonicalClusterPeer(raw)
		if _, ok := seen[peer]; ok {
			continue
		}
		seen[peer] = struct{}{}
		peers = append(peers, peer)
	}
	sortStrings(peers)
	listen, _ := validateClusterListenAddress(src.Listen)
	return &CacheConfig{
		Mode:      cacheModeCluster,
		Listen:    listen,
		Advertise: self,
		Peers:     peers,
		Namespace: strings.TrimSpace(src.Namespace),
		Secret:    src.Secret,
	}, nil
}

func cloneCacheConfig(src *CacheConfig) *CacheConfig {
	if src == nil {
		return nil
	}
	dst := *src
	dst.Peers = append([]string(nil), src.Peers...)
	return &dst
}

func parseCacheCaddyfile(d *caddyfile.Dispenser, dst **CacheConfig) error {
	cfg := &CacheConfig{}
	switch d.CountRemainingArgs() {
	case 0:
	case 1:
		if !d.Args(&cfg.Mode) {
			return d.ArgErr()
		}
	default:
		return d.Errf("expected cache mode or a cache block")
	}

	for nesting := d.Nesting(); d.NextBlock(nesting); {
		switch d.Val() {
		case "mode":
			if !d.Args(&cfg.Mode) {
				return d.Errf("expected exactly one argument for cache mode")
			}
		case "listen":
			if !d.Args(&cfg.Listen) {
				return d.Errf("expected exactly one argument for cache listen")
			}
		case "advertise":
			if !d.Args(&cfg.Advertise) {
				return d.Errf("expected exactly one argument for cache advertise")
			}
		case "peer":
			var peer string
			if !d.Args(&peer) {
				return d.Errf("expected exactly one argument for cache peer")
			}
			cfg.Peers = append(cfg.Peers, peer)
		case "peers":
			peers := d.RemainingArgs()
			if len(peers) == 0 {
				return d.Errf("expected one or more cache peers")
			}
			cfg.Peers = append(cfg.Peers, peers...)
		case "namespace":
			if !d.Args(&cfg.Namespace) {
				return d.Errf("expected exactly one argument for cache namespace")
			}
		case "secret":
			if !d.Args(&cfg.Secret) {
				return d.Errf("expected exactly one argument for cache secret")
			}
		default:
			return d.Errf("unknown cache subdirective: %s", d.Val())
		}
	}
	if strings.TrimSpace(cfg.Mode) == "" {
		// A cache block with topology fields is an ergonomic shorthand for cluster mode.
		cfg.Mode = cacheModeCluster
	}
	*dst = cfg
	return nil
}

func buildCacheConfigFromCLI(mode, listen, advertise, namespace, secret string, peers []string) (*CacheConfig, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		if listen == "" && advertise == "" && namespace == "" && secret == "" && len(peers) == 0 {
			return nil, nil
		}
		mode = cacheModeCluster
	}
	cfg := &CacheConfig{
		Mode:      mode,
		Listen:    listen,
		Advertise: advertise,
		Peers:     append([]string(nil), peers...),
		Namespace: namespace,
		Secret:    secret,
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func listenClusterPeer(ctx context.Context, address string) (net.Listener, error) {
	parsed, err := caddy.ParseNetworkAddress(address)
	if err != nil {
		return nil, err
	}
	if parsed.Network != "tcp" && parsed.Network != "tcp4" && parsed.Network != "tcp6" {
		return nil, fmt.Errorf("cache cluster listener must use TCP")
	}
	if parsed.PortRangeSize() != 1 {
		return nil, fmt.Errorf("cache cluster listener requires exactly one port")
	}
	listener, err := parsed.Listen(ctx, 0, net.ListenConfig{})
	if err != nil {
		return nil, err
	}
	ln, ok := listener.(net.Listener)
	if !ok {
		return nil, fmt.Errorf("cache cluster address did not create a stream listener")
	}
	return ln, nil
}

func startCacheServerForIsolationWithConfig(ctx context.Context, isolation *IsolationConfig, cfg *CacheConfig) (*cacheServer, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	srv, err := startCacheServerForIsolation(isolation)
	if err != nil {
		return nil, err
	}
	if cfg == nil || cfg.effectiveMode() == cacheModeLocal {
		return srv, nil
	}
	listener, err := listenClusterPeer(ctx, cfg.Listen)
	if err != nil {
		_ = srv.Close()
		return nil, fmt.Errorf("cache cluster listen: %w", err)
	}
	if err := srv.attachCluster(cfg, listener); err != nil {
		_ = listener.Close()
		_ = srv.Close()
		return nil, err
	}
	return srv, nil
}

type clusterCache struct {
	store     *cacheStore
	self      string
	peers     []string
	namespace string
	ringID    string
	secret    string
	timeout   time.Duration
}

func newClusterCache(store *cacheStore, cfg *CacheConfig) (*clusterCache, error) {
	normalized, err := normalizeClusterConfig(cfg)
	if err != nil {
		return nil, err
	}
	if normalized == nil {
		return nil, nil
	}
	ringHash := sha256.Sum256([]byte(strings.Join(normalized.Peers, "\x00")))
	return &clusterCache{
		store:     store,
		self:      normalized.Advertise,
		peers:     normalized.Peers,
		namespace: normalized.Namespace,
		ringID:    hex.EncodeToString(ringHash[:]),
		secret:    normalized.Secret,
		timeout:   clusterPeerTimeout,
	}, nil
}

func (c *clusterCache) owner(key []byte) string {
	var owner string
	var best uint64
	for _, peer := range c.peers {
		h := sha256.New()
		_, _ = h.Write([]byte(c.namespace))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(key)
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(peer))
		score := binary.BigEndian.Uint64(h.Sum(nil)[:8])
		if owner == "" || score > best || (score == best && peer < owner) {
			owner, best = peer, score
		}
	}
	return owner
}

func (c *clusterCache) Get(key []byte) ([]byte, bool, error) {
	owner := c.owner(key)
	if owner == c.self {
		scalar, _, _, kind, ok := c.store.Get(key)
		if ok && kind != entryScalar {
			return nil, false, errWrongType
		}
		return scalar, ok, nil
	}
	return c.remoteGet(owner, key)
}

func (c *clusterCache) Set(key, value []byte, ttlSec int64) error {
	owner := c.owner(key)
	if owner == c.self {
		return c.store.Set(key, value, ttlSec)
	}
	parts := [][]byte{[]byte("CSSET"), key, value}
	if ttlSec > 0 {
		parts = append(parts, []byte(strconv.FormatInt(ttlSec, 10)))
	}
	reply, err := c.roundTrip(owner, parts)
	if err != nil {
		return err
	}
	if reply.kind != '+' || reply.text != "OK" {
		return fmt.Errorf("cache cluster peer %s returned an invalid CSSET response", owner)
	}
	return nil
}

func (c *clusterCache) Delete(key []byte) (int, error) {
	owner := c.owner(key)
	if owner == c.self {
		return c.store.Delete(key), nil
	}
	reply, err := c.roundTrip(owner, [][]byte{[]byte("CSDEL"), key})
	if err != nil {
		return 0, err
	}
	if reply.kind != ':' || (reply.integer != 0 && reply.integer != 1) {
		return 0, fmt.Errorf("cache cluster peer %s returned an invalid CSDEL response", owner)
	}
	if reply.integer == 1 {
		return 1, nil
	}
	return 0, nil
}

func (c *clusterCache) remoteGet(owner string, key []byte) ([]byte, bool, error) {
	reply, err := c.roundTrip(owner, [][]byte{[]byte("CSGET"), key})
	if err != nil {
		return nil, false, err
	}
	if reply.kind != '$' {
		return nil, false, fmt.Errorf("cache cluster peer %s returned an invalid CSGET response", owner)
	}
	if reply.null {
		return nil, false, nil
	}
	return reply.bulk, true, nil
}

type clusterReply struct {
	kind    byte
	text    string
	bulk    []byte
	integer int64
	null    bool
}

func (c *clusterCache) roundTrip(peer string, command [][]byte) (clusterReply, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", peer)
	if err != nil {
		return clusterReply{}, fmt.Errorf("cache cluster peer %s: %w", peer, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(c.timeout))
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	if err := respWriteCommand(w, [][]byte{[]byte(clusterPeerAuthCommand), []byte(c.namespace), []byte(c.ringID), []byte(c.secret)}); err != nil {
		return clusterReply{}, fmt.Errorf("cache cluster peer %s auth write: %w", peer, err)
	}
	auth, err := readClusterReply(r)
	if err != nil {
		return clusterReply{}, fmt.Errorf("cache cluster peer %s auth: %w", peer, err)
	}
	if auth.kind != '+' || auth.text != "OK" {
		return clusterReply{}, fmt.Errorf("cache cluster peer %s rejected authentication", peer)
	}
	if err := respWriteCommand(w, command); err != nil {
		return clusterReply{}, fmt.Errorf("cache cluster peer %s write: %w", peer, err)
	}
	reply, err := readClusterReply(r)
	if err != nil {
		return clusterReply{}, fmt.Errorf("cache cluster peer %s: %w", peer, err)
	}
	return reply, nil
}

func readClusterReply(r *bufio.Reader) (clusterReply, error) {
	line, err := respReadLine(r)
	if err != nil {
		return clusterReply{}, err
	}
	if len(line) == 0 {
		return clusterReply{}, errors.New("empty response")
	}
	reply := clusterReply{kind: line[0]}
	switch line[0] {
	case '+':
		reply.text = string(line[1:])
		return reply, nil
	case '-':
		return clusterReply{}, errors.New(strings.TrimSpace(string(line[1:])))
	case ':':
		reply.integer, err = strconv.ParseInt(string(line[1:]), 10, 64)
		return reply, err
	case '$':
		n, parseErr := strconv.Atoi(string(line[1:]))
		if parseErr != nil || n < -1 || n > maxCacheScalarLen {
			return clusterReply{}, errors.New("invalid bulk response length")
		}
		if n == -1 {
			reply.null = true
			return reply, nil
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return clusterReply{}, err
		}
		if buf[n] != '\r' || buf[n+1] != '\n' {
			return clusterReply{}, errors.New("invalid bulk response trailer")
		}
		reply.bulk = buf[:n]
		return reply, nil
	default:
		return clusterReply{}, fmt.Errorf("unsupported response type %q", line[0])
	}
}

func respWriteCommand(w *bufio.Writer, parts [][]byte) error {
	if _, err := fmt.Fprintf(w, "*%d\r\n", len(parts)); err != nil {
		return err
	}
	for _, part := range parts {
		if _, err := fmt.Fprintf(w, "$%d\r\n", len(part)); err != nil {
			return err
		}
		if _, err := w.Write(part); err != nil {
			return err
		}
		if _, err := io.WriteString(w, "\r\n"); err != nil {
			return err
		}
	}
	return w.Flush()
}

func (s *cacheServer) attachCluster(cfg *CacheConfig, listener net.Listener) error {
	cluster, err := newClusterCache(s.store, cfg)
	if err != nil {
		return err
	}
	if cluster == nil {
		return nil
	}
	s.cluster = cluster
	s.peerLn = listener
	s.peerDone = make(chan struct{})
	go s.peerAcceptLoop()
	return nil
}

func (s *cacheServer) peerAcceptLoop() {
	defer close(s.peerDone)
	for {
		conn, err := s.peerLn.Accept()
		if err != nil {
			return
		}
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetNoDelay(true)
		}
		go s.handlePeerConn(conn)
	}
}

func (s *cacheServer) handlePeerConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(clusterPeerTimeout))
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	authenticated := false
	for {
		parts, err := respReadArray(r)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				_ = respWriteError(w, err.Error())
			}
			return
		}
		cmd := strings.ToUpper(string(parts[0]))
		if !authenticated {
			if cmd != clusterPeerAuthCommand || len(parts) != 4 ||
				subtle.ConstantTimeCompare(parts[1], []byte(s.cluster.namespace)) != 1 ||
				subtle.ConstantTimeCompare(parts[2], []byte(s.cluster.ringID)) != 1 ||
				subtle.ConstantTimeCompare(parts[3], []byte(s.cluster.secret)) != 1 {
				_ = respWriteError(w, "NOAUTH Authentication required")
				return
			}
			authenticated = true
			_ = respWriteSimpleString(w, "OK")
			continue
		}
		switch cmd {
		case "CSQUIT":
			_ = respWriteSimpleString(w, "OK")
			return
		case "CSGET":
			if len(parts) != 2 {
				_ = respWriteError(w, "wrong number of arguments for CSGET")
				return
			}
			if s.cluster.owner(parts[1]) != s.cluster.self {
				_ = respWriteError(w, "not owner")
				continue
			}
			scalar, _, _, kind, ok := s.store.Get(parts[1])
			if !ok {
				_ = respWriteBulk(w, nil)
				continue
			}
			if kind != entryScalar {
				_ = respWriteError(w, errWrongType.Error())
				continue
			}
			_ = respWriteBulk(w, scalar)
		case "CSSET":
			if len(parts) != 3 && len(parts) != 4 {
				_ = respWriteError(w, "wrong number of arguments for CSSET")
				return
			}
			if s.cluster.owner(parts[1]) != s.cluster.self {
				_ = respWriteError(w, "not owner")
				continue
			}
			var ttl int64
			if len(parts) == 4 {
				ttl, err = strconv.ParseInt(string(parts[3]), 10, 64)
				if err != nil || ttl < 0 {
					_ = respWriteError(w, "invalid TTL")
					return
				}
			}
			if err := s.store.Set(parts[1], parts[2], ttl); err != nil {
				_ = respWriteError(w, err.Error())
				continue
			}
			_ = respWriteSimpleString(w, "OK")
		case "CSDEL":
			if len(parts) != 2 {
				_ = respWriteError(w, "wrong number of arguments for CSDEL")
				return
			}
			if s.cluster.owner(parts[1]) != s.cluster.self {
				_ = respWriteError(w, "not owner")
				continue
			}
			_ = respWriteInt(w, int64(s.store.Delete(parts[1])))
		default:
			_ = respWriteError(w, clusterUnsupportedError)
		}
	}
}
