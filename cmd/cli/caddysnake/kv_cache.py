"""RESP2 client for the Caddy in-process shared worker cache.

Python workers connect using the environment variable ``CADDYSNAKE_CACHE_ADDR``
(``unix://...`` on Unix, ``127.0.0.1:port`` on Windows). Use ``Cache`` or the module-level
``cache`` singleton when that variable is set (typically process workers under Caddy).

Each key stores either a **scalar** (one blob of bytes) or a **list** (FIFO queue of
blobs). Method docstrings on ``Cache`` describe how operations behave in each mode.

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


class CacheError(RuntimeError):
    """Cache server returned an error or the connection failed."""


class CacheConfigurationError(CacheError):
    """Cache is not available (missing env or wrong runtime)."""


def _socket_module():
    iface = (os.environ.get(ENV_IFACE) or "").lower()
    if iface == "esgi":
        try:
            from gevent import socket as gsocket  # type: ignore import-not-found

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

    **Scalar vs list:** ``set`` stores a **scalar**. ``append`` builds or extends a
    **list**; if the key held a scalar, it becomes ``[old_scalar, new_chunk]``. ``get``
    returns ``bytes`` for a scalar, ``list[bytes]`` for a list (possibly empty), or
    ``None`` if the key is missing or expired. ``pop`` only applies to **list** keys
    (FIFO); it returns ``None`` for scalars, missing keys, or when a timed wait expires
    before an element arrives.

    **Errors:** Failures from the server or network raise ``CacheError``. Missing
    ``CADDYSNAKE_CACHE_ADDR`` (or bad address) raises ``CacheConfigurationError``.

    **Async:** ``aset``, ``aget``, ``adelete``, ``aappend``, and ``apop`` run the
    blocking counterparts in ``asyncio.to_thread`` for use in ASGI code.

    Example::

        from caddysnake import cache

        cache.set("user:42:session", session_bytes, ttl=3600)
        raw = cache.get("user:42:session")

        cache.append("jobs", b"task-1")
        cache.append("jobs", b"task-2")
        first = cache.pop("jobs")
    """

    __slots__ = ()

    def _open_socket(self, mod):
        addr = _require_addr()
        if addr.startswith("unix://"):
            path = addr[7:]
            # ESGI uses gevent for the Go-facing server, but gevent's client
            # AF_UNIX path has been unreliable; stdlib socket is fine here (short
            # local IPC and does not block other greenlets' I/O meaningfully).
            import socket as stdsocket

            sock = stdsocket.socket(stdsocket.AF_UNIX, stdsocket.SOCK_STREAM)
            sock.settimeout(_timeout_sec())
            try:
                sock.connect(path)
            except OSError as e:
                sock.close()
                raise CacheError(
                    f"cannot connect to cache unix socket {path!r}: {e}"
                ) from e
            return sock
        host, port = _parse_tcp_host_port(addr)
        sock = mod.socket(mod.AF_INET, mod.SOCK_STREAM)
        sock.settimeout(_timeout_sec())
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

    def set(self, key: str | bytes, value: str | bytes, ttl: int | None = None) -> None:
        """Store a scalar value under ``key``, replacing any previous value (scalar or list).

        After ``set``, the key is a **scalar** until ``append`` is used.

        Args:
            key: Cache key (UTF-8 if ``str``).
            value: Value to store (UTF-8 if ``str``).
            ttl: Optional time-to-live in **seconds** (integer). Omit or ``None`` for no
                expiry on this key (until deleted or overwritten).

        Raises:
            CacheError: Server rejected the operation (limits, I/O, etc.).
        """
        kb = _to_bytes(key)
        vb = _to_bytes(value)
        if ttl is None:
            self._roundtrip([b"CSSET", kb, vb])
        else:
            self._roundtrip([b"CSSET", kb, vb, str(int(ttl)).encode()])

    def get(self, key: str | bytes) -> bytes | list[bytes] | None:
        """Return the value at ``key``, or ``None`` if missing or expired.

        Returns:
            * ``bytes`` — key holds a **scalar** (via ``set``).
            * ``list[bytes]`` — key holds a **list** (via ``append``); may be empty.
            * ``None`` — no such key, expired, or invalid key per server limits.

        Raises:
            CacheError: Server or protocol error.
        """
        r = self._roundtrip([b"CSGET", _to_bytes(key)])
        if r is None:
            return None
        if isinstance(r, list):
            return r
        return r

    def delete(self, key: str | bytes) -> int:
        """Remove ``key`` if it exists.

        Returns:
            ``1`` if a key was removed, ``0`` if it was already absent or expired.

        Raises:
            CacheError: Server or protocol error.
        """
        return int(self._roundtrip([b"CSDEL", _to_bytes(key)]))

    def append(self, key: str | bytes, value: str | bytes) -> None:
        """Append a chunk to the **list** at ``key``.

        If the key does not exist, starts a one-element list. If it holds a **scalar**,
        the scalar and ``value`` become the first two list elements. **TTL is cleared**
        when a key becomes or stays a list.

        Raises:
            CacheError: Server rejected the operation (e.g. list size/value limits).
        """
        self._roundtrip([b"CSAPPEND", _to_bytes(key), _to_bytes(value)])

    def pop(self, key: str | bytes, timeout: float | None = None) -> bytes | None:
        """Pop the **first** element from the list at ``key`` (FIFO).

        Blocks only when ``timeout`` is set: waits up to that many **seconds** for an
        element to appear. Another worker can ``append`` while you wait.

        Returns:
            * ``bytes`` — first queued element was removed and returned.
            * ``None`` — key missing, expired, key holds a **scalar** (not a list), list
              empty at observation time, or ``timeout`` elapsed with no element.

        Args:
            key: Cache key.
            timeout: Maximum seconds to wait for a list element; ``None`` returns
                immediately (non-blocking pop).

        Raises:
            CacheError: Server or protocol error.
        """
        kb = _to_bytes(key)
        if timeout is None:
            r = self._roundtrip([b"CSPOP", kb])
        else:
            r = self._roundtrip([b"CSPOP", kb, str(float(timeout)).encode()])
        return r

    async def aset(
        self, key: str | bytes, value: str | bytes, ttl: int | None = None
    ) -> None:
        """Async wrapper for ``set`` (runs in a worker thread via ``asyncio.to_thread``)."""
        await asyncio.to_thread(self.set, key, value, ttl)

    async def aget(self, key: str | bytes) -> bytes | list[bytes] | None:
        """Async wrapper for ``get`` (``asyncio.to_thread``)."""
        return await asyncio.to_thread(self.get, key)

    async def adelete(self, key: str | bytes) -> int:
        """Async wrapper for ``delete`` (``asyncio.to_thread``)."""
        return await asyncio.to_thread(self.delete, key)

    async def aappend(self, key: str | bytes, value: str | bytes) -> None:
        """Async wrapper for ``append`` (``asyncio.to_thread``)."""
        await asyncio.to_thread(self.append, key, value)

    async def apop(
        self, key: str | bytes, timeout: float | None = None
    ) -> bytes | None:
        """Async wrapper for ``pop`` (``asyncio.to_thread``).

        The blocking wait still runs in a thread; tune ``timeout`` to bound wait time.
        """
        return await asyncio.to_thread(self.pop, key, timeout)


cache = Cache()
