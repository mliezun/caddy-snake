import pytest

from caddysnake.kv_cache import (
    ENV_WORKER_ID,
    Cache,
    CacheConfigurationError,
    CacheError,
    _encode_cmd,
    worker_id,
)


def test_encode_cmd_single_bulk():
    assert _encode_cmd([b"X"]) == b"*1\r\n$1\r\nX\r\n"


def test_encode_csget():
    got = _encode_cmd([b"CSGET", b"key"])
    assert got.startswith(b"*2\r\n")
    assert b"CSGET" in got
    assert b"key" in got


class _FakeSock:
    def __init__(self, to_recv: bytes):
        self._data = to_recv
        self.sent = b""
        self.timeout = None

    def settimeout(self, t):
        self.timeout = t

    def connect(self, _addr):
        pass

    def sendall(self, data):
        self.sent = data

    def recv(self, n):
        if not self._data:
            return b""
        if len(self._data) <= n:
            chunk = self._data
            self._data = b""
            return chunk
        chunk = self._data[:n]
        self._data = self._data[n:]
        return chunk

    def close(self):
        pass


class _FakeMod:
    AF_INET = 2
    AF_UNIX = 1
    SOCK_STREAM = 1
    _response = b""
    last_sock: _FakeSock | None = None
    last_args: tuple = ()

    @classmethod
    def socket(cls, *a, **_k):
        cls.last_args = a
        cls.last_sock = _FakeSock(cls._response)
        return cls.last_sock


def _patch_cache(monkeypatch):
    monkeypatch.setenv("CADDYSNAKE_CACHE_ADDR", "127.0.0.1:19999")
    monkeypatch.delenv("CADDYSNAKE_WORKER_INTERFACE", raising=False)
    monkeypatch.setattr("caddysnake.kv_cache._socket_module", lambda: _FakeMod)


def _patch_cache_unix(monkeypatch, path="/tmp/caddysnake-cache-test.sock"):
    monkeypatch.setenv("CADDYSNAKE_CACHE_ADDR", f"unix://{path}")
    monkeypatch.setenv("CADDYSNAKE_WORKER_INTERFACE", "esgi")
    monkeypatch.setattr("caddysnake.kv_cache._socket_module", lambda: _FakeMod)


def test_unix_socket_uses_the_selected_socket_module(monkeypatch):
    # ESGI workers get gevent.socket from _socket_module(). The unix:// path must
    # honour it: opening a blocking stdlib socket instead never yields to the gevent
    # hub, freezing every other greenlet in the worker for the whole round-trip.
    # unix:// is the default transport on Unix, so this is the hot path.
    _patch_cache_unix(monkeypatch)
    _FakeMod._response = b"$-1\r\n"
    _FakeMod.last_args = ()
    assert Cache().get("missing") is None
    assert _FakeMod.last_args == (_FakeMod.AF_UNIX, _FakeMod.SOCK_STREAM)


def test_unix_blocking_roundtrip_uses_the_selected_socket_module(monkeypatch):
    # Same requirement for the blocking (wait) variant behind pop/subscribe, which
    # holds the socket for up to max(_timeout_sec(), wait + 5.0) seconds.
    _patch_cache_unix(monkeypatch)
    _FakeMod._response = b"$-1\r\n"
    _FakeMod.last_args = ()
    Cache().pop("q", timeout=0.01)
    assert _FakeMod.last_args == (_FakeMod.AF_UNIX, _FakeMod.SOCK_STREAM)


def test_get_miss_roundtrip(monkeypatch):
    _patch_cache(monkeypatch)
    _FakeMod._response = b"$-1\r\n"
    assert Cache().get("missing") is None


def test_delete_returns_int(monkeypatch):
    _patch_cache(monkeypatch)
    _FakeMod._response = b":1\r\n"
    assert Cache().delete("k") == 1


def test_server_err_raises(monkeypatch):
    _patch_cache(monkeypatch)
    _FakeMod._response = b"-ERR something wrong\r\n"
    with pytest.raises(CacheError, match="something wrong"):
        Cache().get("k")


def test_missing_cache_addr(monkeypatch):
    monkeypatch.delenv("CADDYSNAKE_CACHE_ADDR", raising=False)
    with pytest.raises(CacheConfigurationError):
        Cache().get("x")


@pytest.mark.asyncio
async def test_aget(monkeypatch):
    _patch_cache(monkeypatch)
    _FakeMod._response = b"$3\r\nbye\r\n"
    assert await Cache().aget("k") == b"bye"


def test_cache_set_methods_encode_commands(monkeypatch):
    _patch_cache(monkeypatch)
    _FakeMod._response = b":1\r\n"
    Cache().sadd("g", b"m")
    assert _FakeMod.last_sock is not None and b"CSSADD" in _FakeMod.last_sock.sent
    _FakeMod._response = b":1\r\n"
    Cache().srem("g", b"m")
    assert _FakeMod.last_sock is not None and b"CSSREM" in _FakeMod.last_sock.sent
    _FakeMod._response = b"*1\r\n$1\r\nm\r\n"
    assert Cache().smembers("g") == [b"m"]


def test_cache_set_members_empty_array(monkeypatch):
    _patch_cache(monkeypatch)
    _FakeMod._response = b"*0\r\n"
    assert Cache().smembers("missing") == []


def test_cache_setnx_return_bool(monkeypatch):
    _patch_cache(monkeypatch)
    _FakeMod._response = b":1\r\n"
    assert Cache().setnx("k", b"v") is True
    _FakeMod._response = b":0\r\n"
    assert Cache().setnx("k", b"v") is False


def test_cache_setnx_ttl_argument_encoding(monkeypatch):
    _patch_cache(monkeypatch)
    _FakeMod._response = b":1\r\n"
    Cache().setnx("k", b"v", ttl=60)
    assert _FakeMod.last_sock is not None
    assert b"60" in _FakeMod.last_sock.sent


def test_cache_keys_pattern_encoding_and_reply(monkeypatch):
    _patch_cache(monkeypatch)
    _FakeMod._response = b"*2\r\n$3\r\napp\r\n$4\r\napp2\r\n"
    keys = Cache().keys("app", limit=10)
    assert keys == [b"app", b"app2"]
    assert _FakeMod.last_sock is not None
    assert b"CSKEYS" in _FakeMod.last_sock.sent


def test_cache_publish_subscribe_commands(monkeypatch):
    _patch_cache(monkeypatch)
    _FakeMod._response = b":2\r\n"
    assert Cache().publish("ch", b"msg") == 2
    _FakeMod._response = b"$3\r\nmsg\r\n"
    assert Cache().subscribe("ch", timeout=1.0) == b"msg"


def test_cache_subscribe_timeout_and_eof_errors(monkeypatch):
    _patch_cache(monkeypatch)
    _FakeMod._response = b"$-1\r\n"
    assert Cache().subscribe("ch", timeout=0.1) is None
    with pytest.raises(ValueError):
        Cache().subscribe("ch", timeout=0)


def test_worker_id_environment_accessor(monkeypatch):
    monkeypatch.delenv(ENV_WORKER_ID, raising=False)
    assert worker_id() is None
    monkeypatch.setenv(ENV_WORKER_ID, "2")
    assert worker_id() == 2
    monkeypatch.setenv(ENV_WORKER_ID, "bad")
    assert worker_id() is None


@pytest.mark.asyncio
async def test_async_wrappers_for_new_methods(monkeypatch):
    _patch_cache(monkeypatch)
    _FakeMod._response = b":1\r\n"
    c = Cache()
    assert await c.asadd("g", b"a") == 1
    _FakeMod._response = b":1\r\n"
    assert await c.asrem("g", b"a") == 1
    _FakeMod._response = b"*0\r\n"
    assert await c.asmembers("g") == []
    _FakeMod._response = b":1\r\n"
    assert await c.asetnx("k", b"v") is True
    _FakeMod._response = b"*0\r\n"
    assert await c.akeys("p") == []
    _FakeMod._response = b":0\r\n"
    assert await c.apublish("c", b"m") == 0
    _FakeMod._response = b"$-1\r\n"
    assert await c.asubscribe("c", timeout=0.01) is None


def test_new_methods_require_cache_addr(monkeypatch):
    monkeypatch.delenv("CADDYSNAKE_CACHE_ADDR", raising=False)
    c = Cache()
    with pytest.raises(CacheConfigurationError):
        c.sadd("k", b"v")
    with pytest.raises(CacheConfigurationError):
        c.srem("k", b"v")
    with pytest.raises(CacheConfigurationError):
        c.smembers("k")
    with pytest.raises(CacheConfigurationError):
        c.setnx("k", b"v")
    with pytest.raises(CacheConfigurationError):
        c.keys("p")
    with pytest.raises(CacheConfigurationError):
        c.publish("c", b"m")
    with pytest.raises(CacheConfigurationError):
        c.subscribe("c", timeout=1.0)


def test_partial_resp_reads_for_new_array_replies(monkeypatch):
    _patch_cache(monkeypatch)
    _FakeMod._response = b"*1\r\n$1\r\n"
    _FakeMod._response += b"a\r\n"
    assert Cache().smembers("g") == [b"a"]
