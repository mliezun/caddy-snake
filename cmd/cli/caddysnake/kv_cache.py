"""RESP2 client for the Caddy in-process shared worker cache.

Python workers connect using the environment variable ``CADDYSNAKE_CACHE_ADDR``
(``unix://...`` on Unix, ``127.0.0.1:port`` on Windows). Use ``Cache`` or the module-level
``cache`` singleton when that variable is set (typically process workers under Caddy).

Each key stores either a **scalar** (one blob of bytes), a **list** (FIFO queue of
blobs), or a **set** (unique member blobs). Method docstrings on ``Cache`` describe
how operations behave in each mode.

The ``cache`` name exported from this module is a single shared ``Cache()`` instance for
convenience (same methods as the class).
"""

from __future__ import annotations

import asyncio
import os
from typing import Any

ENV_ADDR = "CADDYSNAKE_CACHE_ADDR"
ENV_IFACE = "CADDYSNAKE_WORKER_INTERFACE"
ENV_TIMEOUT = "CADDYSNAKE_CACHE_TIMEOUT"
ENV_WORKER_ID = "CADDYSNAKE_WORKER_ID"


class CacheError(RuntimeError):
    """Cache server returned an error or the connection failed."""


class CacheConfigurationError(CacheError):
    """Cache is not available (missing env or wrong runtime)."""


def worker_id() -> int | None:
    """Return ``CADDYSNAKE_WORKER_ID`` as an int, or ``None`` if unset or invalid."""
    raw = os.environ.get(ENV_WORKER_ID)
    if raw is None or raw == "":
        return None
    try:
        return int(raw)
    except ValueError:
        return None


def _socket_module():
    iface = (os.environ.get(ENV_IFACE) or "").lower()
    if iface == "esgi":
        try:
            from gevent import socket as gsocket

            return gsocket
        except ImportError as e:
            raise CacheConfigurationError(
                "ESGI workers need gevent installed to use caddysnake.cache (gevent.socket)"
            ) from e
    import socket as stdsocket

    return stdsocket


def _timeout_sec() -> float:
    raw = os.environ.get(ENV_TIMEOUT)
    if raw is None or raw == "":
        return 30.0
    try:
        return float(raw)
    except ValueError:
        return 30.0


def _require_addr() -> str:
    a = os.environ.get(ENV_ADDR)
    if not a:
        raise CacheConfigurationError(
            "CADDYSNAKE_CACHE_ADDR is not set; run behind Caddy with caddy-snake "
            "to use the centralized cache."
        )
    return a


def _parse_tcp_host_port(addr: str) -> tuple[str, int]:
    """Parse host:port for the Windows TCP cache listener (IPv4 or bracketed IPv6)."""
    if addr.startswith("["):
        try:
            br = addr.index("]:")
            host = addr[1:br]
            port = int(addr[br + 2 :])
        except ValueError as e:
            raise CacheConfigurationError(f"invalid TCP cache address {addr!r}") from e
        return host, port
    if ":" in addr:
        host, _, port_s = addr.rpartition(":")
        if host and port_s.isdigit():
            return host, int(port_s)
    raise CacheConfigurationError(f"invalid TCP cache address {addr!r}")


def _to_bytes(x: str | bytes) -> bytes:
    if isinstance(x, bytes):
        return x
    return str(x).encode("utf-8")


def _encode_bulk(b: bytes) -> bytes:
    return b"$%d\r\n" % len(b) + b + b"\r\n"


def _encode_cmd(parts: list[bytes]) -> bytes:
    out = bytearray(b"*%d\r\n" % len(parts))
    for p in parts:
        out.extend(_encode_bulk(p))
    return bytes(out)


def _recv_some(sock, buf: bytearray, n: int) -> None:
    while len(buf) < n:
        chunk = sock.recv(max(n - len(buf), 1))
        if not chunk:
            raise CacheError("unexpected EOF from cache")
        buf.extend(chunk)


def _recv_line(sock, buf: bytearray) -> bytes:
    while True:
        i = buf.find(b"\r\n")
        if i != -1:
            line = bytes(buf[:i])
            del buf[: i + 2]
            return line
        chunk = sock.recv(4096)
        if not chunk:
            raise CacheError("unexpected EOF from cache")
        buf.extend(chunk)


def _read_bulk_payload(sock, buf: bytearray, n: int) -> bytes:
    need = n + 2
    _recv_some(sock, buf, need)
    raw = bytes(buf[:need])
    del buf[:need]
    if raw[n : n + 2] != b"\r\n":
        raise CacheError("invalid bulk payload from cache")
    return raw[:n]


def _read_reply(sock, buf: bytearray) -> Any:
    line = _recv_line(sock, buf)
    if not line:
        raise CacheError("empty reply from cache")
    if line[0:1] == b"+":
        return line[1:].decode("utf-8", "replace")
    if line[0:1] == b"-":
        msg = line[1:].decode("utf-8", "replace").strip()
        raise CacheError(msg)
    if line[0:1] == b":":
        return int(line[1:])
    if line[0:1] == b"$":
        n = int(line[1:])
        if n == -1:
            return None
        return _read_bulk_payload(sock, buf, n)
    if line[0:1] == b"*":
        n = int(line[1:])
        out: list[bytes] = []
        for _ in range(n):
            ln = _recv_line(sock, buf)
            if ln[0:1] != b"$":
                raise CacheError("expected bulk inside array")
            elem_n = int(ln[1:])
            if elem_n < 0:
                raise CacheError("unexpected null element in cache array")
            out.append(_read_bulk_payload(sock, buf, elem_n))
        return out
    raise CacheError(f"unrecognized RESP from cache: {line!r}")


class Cache:
    """Synchronous and asyncio helpers for the shared in-process Caddy cache.

    Each operation opens a connection, sends one RESP command, reads the reply, and
    closes. Suitable for small payloads and cross-worker coordination; not a substitute
    for Redis or a database (see the docs).

    **Scalar vs list vs set:** ``set`` stores a **scalar**. ``append`` builds or extends a
    **list**. ``sadd``/``srem``/``smembers`` operate on **sets**. ``get`` returns ``bytes``
    for a scalar, ``list[bytes]`` for a list, or ``None`` if missing or expired.

    **Blocking receive:** ``pop`` and ``subscribe`` block on the server when ``timeout`` is
    set. ``subscribe`` requires a timeout (one-shot blocking receive, not persistent
    Redis-style pub/sub).

    **Errors:** Failures from the server or network raise ``CacheError``. Missing
    ``CADDYSNAKE_CACHE_ADDR`` raises ``CacheConfigurationError``.
    """

    __slots__ = ()

    def _open_socket(self, mod, timeout: float | None = None):
        addr = _require_addr()
        t = _timeout_sec() if timeout is None else timeout
        if addr.startswith("unix://"):
            path = addr[7:]
            # Must use ``mod`` (gevent.socket for ESGI workers), never the stdlib
            # socket: a blocking stdlib call here does not yield to the gevent hub,
            # which freezes every other greenlet in the worker for the duration of
            # the cache round-trip. gevent.socket supports AF_UNIX — the ESGI
            # listener itself uses it (see run_esgi_server in caddysnake.py).
            sock = mod.socket(mod.AF_UNIX, mod.SOCK_STREAM)
            sock.settimeout(t)
            try:
                sock.connect(path)
            except OSError as e:
                sock.close()
                raise CacheError(f"cannot connect to cache unix socket {path!r}: {e}") from e
            return sock
        host, port = _parse_tcp_host_port(addr)
        sock = mod.socket(mod.AF_INET, mod.SOCK_STREAM)
        sock.settimeout(t)
        try:
            sock.connect((host, port))
        except OSError as e:
            sock.close()
            raise CacheError(f"cannot connect to cache at {host!r}:{port}: {e}") from e
        return sock

    def _roundtrip(self, parts: list[bytes]) -> Any:
        mod = _socket_module()
        sock = self._open_socket(mod)
        buf = bytearray()
        try:
            sock.sendall(_encode_cmd(parts))
            return _read_reply(sock, buf)
        finally:
            sock.close()

    def _roundtrip_blocking(self, parts: list[bytes], wait_sec: float) -> Any:
        mod = _socket_module()
        sock_timeout = max(_timeout_sec(), wait_sec + 5.0)
        sock = self._open_socket(mod, timeout=sock_timeout)
        buf = bytearray()
        try:
            sock.sendall(_encode_cmd(parts))
            return _read_reply(sock, buf)
        finally:
            sock.close()

    def set(self, key: str | bytes, value: str | bytes, ttl: int | None = None) -> None:
        """Store a scalar value under ``key``, replacing any previous value."""
        kb = _to_bytes(key)
        vb = _to_bytes(value)
        if ttl is None:
            self._roundtrip([b"CSSET", kb, vb])
        else:
            self._roundtrip([b"CSSET", kb, vb, str(int(ttl)).encode()])

    def get(self, key: str | bytes) -> bytes | list[bytes] | None:
        """Return the value at ``key``, or ``None`` if missing or expired."""
        r = self._roundtrip([b"CSGET", _to_bytes(key)])
        if r is None:
            return None
        if isinstance(r, list):
            return r
        return r

    def delete(self, key: str | bytes) -> int:
        """Remove ``key`` if it exists. Returns ``1`` if removed, ``0`` otherwise."""
        return int(self._roundtrip([b"CSDEL", _to_bytes(key)]))

    def append(self, key: str | bytes, value: str | bytes) -> None:
        """Append a chunk to the **list** at ``key``."""
        self._roundtrip([b"CSAPPEND", _to_bytes(key), _to_bytes(value)])

    def pop(self, key: str | bytes, timeout: float | None = None) -> bytes | None:
        """Pop the **first** element from the list at ``key`` (FIFO)."""
        kb = _to_bytes(key)
        if timeout is None:
            r = self._roundtrip([b"CSPOP", kb])
        else:
            r = self._roundtrip_blocking(
                [b"CSPOP", kb, str(float(timeout)).encode()], float(timeout)
            )
        return r

    def sadd(self, key: str | bytes, member: str | bytes) -> int:
        """Add ``member`` to the **set** at ``key``. Returns ``1`` if new, ``0`` if present."""
        return int(self._roundtrip([b"CSSADD", _to_bytes(key), _to_bytes(member)]))

    def srem(self, key: str | bytes, member: str | bytes) -> int:
        """Remove ``member`` from the set at ``key``. Returns ``1`` if removed, ``0`` if absent."""
        return int(self._roundtrip([b"CSSREM", _to_bytes(key), _to_bytes(member)]))

    def smembers(self, key: str | bytes) -> list[bytes]:
        """Return all members of the set at ``key`` (empty list if missing)."""
        r = self._roundtrip([b"CSSMEMBERS", _to_bytes(key)])
        if not isinstance(r, list):
            return []
        return r

    def setnx(self, key: str | bytes, value: str | bytes, ttl: int | None = None) -> bool:
        """Set scalar ``key`` only if absent. Returns ``True`` if set, ``False`` if exists."""
        kb = _to_bytes(key)
        vb = _to_bytes(value)
        if ttl is None:
            n = self._roundtrip([b"CSSETNX", kb, vb])
        else:
            n = self._roundtrip([b"CSSETNX", kb, vb, str(int(ttl)).encode()])
        return int(n) == 1

    def keys(self, prefix: str | bytes, limit: int = 1000) -> list[bytes]:
        """List key names matching ``prefix`` (up to ``limit``, max 1000).

        ``prefix`` must be non-empty (server rejects full keyspace scans).
        """
        pb = _to_bytes(prefix)
        if not pb:
            raise ValueError("keys prefix must be non-empty")
        r = self._roundtrip([b"CSKEYS", pb, str(int(limit)).encode()])
        if not isinstance(r, list):
            return []
        return r

    def publish(self, channel: str | bytes, message: str | bytes) -> int:
        """Deliver ``message`` to waiters blocked on ``channel``. Returns waiter count."""
        return int(self._roundtrip([b"CSPUBLISH", _to_bytes(channel), _to_bytes(message)]))

    def subscribe(self, channel: str | bytes, timeout: float) -> bytes | None:
        """Block up to ``timeout`` seconds for one message on ``channel`` (required).

        One-shot blocking receive; not a persistent subscription. Returns ``None`` on
        timeout with no message.
        """
        if timeout <= 0:
            raise ValueError("subscribe timeout must be positive")
        r = self._roundtrip_blocking(
            [b"CSSUBSCRIBE", _to_bytes(channel), str(float(timeout)).encode()],
            float(timeout),
        )
        return r

    async def aset(self, key: str | bytes, value: str | bytes, ttl: int | None = None) -> None:
        await asyncio.to_thread(self.set, key, value, ttl)

    async def aget(self, key: str | bytes) -> bytes | list[bytes] | None:
        return await asyncio.to_thread(self.get, key)

    async def adelete(self, key: str | bytes) -> int:
        return await asyncio.to_thread(self.delete, key)

    async def aappend(self, key: str | bytes, value: str | bytes) -> None:
        await asyncio.to_thread(self.append, key, value)

    async def apop(self, key: str | bytes, timeout: float | None = None) -> bytes | None:
        return await asyncio.to_thread(self.pop, key, timeout)

    async def asadd(self, key: str | bytes, member: str | bytes) -> int:
        return await asyncio.to_thread(self.sadd, key, member)

    async def asrem(self, key: str | bytes, member: str | bytes) -> int:
        return await asyncio.to_thread(self.srem, key, member)

    async def asmembers(self, key: str | bytes) -> list[bytes]:
        return await asyncio.to_thread(self.smembers, key)

    async def asetnx(self, key: str | bytes, value: str | bytes, ttl: int | None = None) -> bool:
        return await asyncio.to_thread(self.setnx, key, value, ttl)

    async def akeys(self, prefix: str | bytes = "", limit: int = 1000) -> list[bytes]:
        return await asyncio.to_thread(self.keys, prefix, limit)

    async def apublish(self, channel: str | bytes, message: str | bytes) -> int:
        return await asyncio.to_thread(self.publish, channel, message)

    async def asubscribe(self, channel: str | bytes, timeout: float) -> bytes | None:
        return await asyncio.to_thread(self.subscribe, channel, timeout)


cache = Cache()
