"""RESP2 client for the Caddy in-process caddy-snake cache (Unix socket or TCP)."""

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
    """Process-local façade for the shared Caddy cache (Unix socket or TCP)."""

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
        kb = _to_bytes(key)
        vb = _to_bytes(value)
        if ttl is None:
            self._roundtrip([b"CSSET", kb, vb])
        else:
            self._roundtrip([b"CSSET", kb, vb, str(int(ttl)).encode()])

    def get(self, key: str | bytes) -> bytes | list[bytes] | None:
        r = self._roundtrip([b"CSGET", _to_bytes(key)])
        if r is None:
            return None
        if isinstance(r, list):
            return r
        return r

    def delete(self, key: str | bytes) -> int:
        return int(self._roundtrip([b"CSDEL", _to_bytes(key)]))

    def append(self, key: str | bytes, value: str | bytes) -> None:
        self._roundtrip([b"CSAPPEND", _to_bytes(key), _to_bytes(value)])

    def pop(self, key: str | bytes, timeout: float | None = None) -> bytes | None:
        kb = _to_bytes(key)
        if timeout is None:
            r = self._roundtrip([b"CSPOP", kb])
        else:
            r = self._roundtrip([b"CSPOP", kb, str(float(timeout)).encode()])
        return r

    async def aset(
        self, key: str | bytes, value: str | bytes, ttl: int | None = None
    ) -> None:
        await asyncio.to_thread(self.set, key, value, ttl)

    async def aget(self, key: str | bytes) -> bytes | list[bytes] | None:
        return await asyncio.to_thread(self.get, key)

    async def adelete(self, key: str | bytes) -> int:
        return await asyncio.to_thread(self.delete, key)

    async def aappend(self, key: str | bytes, value: str | bytes) -> None:
        await asyncio.to_thread(self.append, key, value)

    async def apop(
        self, key: str | bytes, timeout: float | None = None
    ) -> bytes | None:
        return await asyncio.to_thread(self.pop, key, timeout)


cache = Cache()
