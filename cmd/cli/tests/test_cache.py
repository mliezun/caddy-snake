import pytest

from caddysnake.kv_cache import (
    Cache,
    CacheConfigurationError,
    CacheError,
    _encode_cmd,
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

    def settimeout(self, _t):
        pass

    def connect(self, _addr):
        pass

    def sendall(self, data):
        self.sent = data

    def recv(self, _n):
        if not self._data:
            return b""
        chunk = self._data
        self._data = b""
        return chunk

    def close(self):
        pass


class _FakeMod:
    AF_INET = 2
    SOCK_STREAM = 1
    _response = b""

    @classmethod
    def socket(cls, *_a, **_k):
        return _FakeSock(cls._response)


def test_get_miss_roundtrip(monkeypatch):
    monkeypatch.setenv("CADDYSNAKE_CACHE_ADDR", "127.0.0.1:19999")
    monkeypatch.delenv("CADDYSNAKE_WORKER_INTERFACE", raising=False)
    _FakeMod._response = b"$-1\r\n"
    monkeypatch.setattr("caddysnake.kv_cache._socket_module", lambda: _FakeMod)
    assert Cache().get("missing") is None


def test_delete_returns_int(monkeypatch):
    monkeypatch.setenv("CADDYSNAKE_CACHE_ADDR", "127.0.0.1:19999")
    _FakeMod._response = b":1\r\n"
    monkeypatch.setattr("caddysnake.kv_cache._socket_module", lambda: _FakeMod)
    assert Cache().delete("k") == 1


def test_server_err_raises(monkeypatch):
    monkeypatch.setenv("CADDYSNAKE_CACHE_ADDR", "127.0.0.1:19999")
    _FakeMod._response = b"-ERR something wrong\r\n"
    monkeypatch.setattr("caddysnake.kv_cache._socket_module", lambda: _FakeMod)
    with pytest.raises(CacheError, match="something wrong"):
        Cache().get("k")


def test_missing_cache_addr(monkeypatch):
    monkeypatch.delenv("CADDYSNAKE_CACHE_ADDR", raising=False)
    with pytest.raises(CacheConfigurationError):
        Cache().get("x")


@pytest.mark.asyncio
async def test_aget(monkeypatch):
    monkeypatch.setenv("CADDYSNAKE_CACHE_ADDR", "127.0.0.1:19999")
    _FakeMod._response = b"$3\r\nbye\r\n"
    monkeypatch.setattr("caddysnake.kv_cache._socket_module", lambda: _FakeMod)
    assert await Cache().aget("k") == b"bye"
