#!/usr/bin/env python3
"""
caddysnake - Self-contained WSGI/ASGI server for caddy-snake.

Listens on a Unix domain socket and serves a Python WSGI or ASGI application.
Designed to be spawned as a subprocess by the caddy-snake Go module.

Usage:
    python caddysnake.py --socket /path/to/sock --app module:variable \
        --interface wsgi|asgi|esgi [--working-dir DIR] [--venv DIR] [--lifespan on|off] [--runtime NAME]
"""

import argparse
import asyncio
import base64
import concurrent.futures
import contextlib
import hashlib
import importlib
import ipaddress
import os
import signal
import struct
import sys
import tempfile
import traceback
from http import HTTPStatus
from urllib.parse import unquote


class ClientDisconnected(Exception):
    """Raised when the downstream client closes the connection mid-response."""


# Errors raised when writing to a transport whose peer has gone away.
# OSError covers ConnectionError/ConnectionResetError/BrokenPipeError. RuntimeError
# is included because uvloop raises it ("unable to perform operation ...; the
# handler is closed") when the underlying socket has already been torn down.
_CLIENT_DISCONNECT_ERRORS = (OSError, RuntimeError)


def setup_paths(working_dir, venv):
    """Configure sys.path for working directory and virtualenv."""
    if working_dir:
        abs_dir = os.path.abspath(working_dir)
        if abs_dir not in sys.path:
            sys.path.insert(0, abs_dir)
        os.chdir(abs_dir)

    if venv:
        if sys.platform == "win32":
            site_packages = os.path.join(venv, "Lib", "site-packages")
        else:
            lib_dir = os.path.join(venv, "lib")
            python_dir = None
            if os.path.isdir(lib_dir):
                for name in sorted(os.listdir(lib_dir)):
                    if name.startswith("python3"):
                        python_dir = name
                        break
            if python_dir:
                site_packages = os.path.join(lib_dir, python_dir, "site-packages")
            else:
                print(
                    f"Warning: could not find python3.* directory in {lib_dir}",
                    file=sys.stderr,
                )
                return

        if os.path.isdir(site_packages):
            if site_packages not in sys.path:
                sys.path.insert(0, site_packages)
        else:
            print(
                f"Warning: site-packages not found at {site_packages}",
                file=sys.stderr,
            )


def import_app(module_app_str):
    """Import and return the app from 'module:variable' format."""
    parts = module_app_str.split(":")
    if len(parts) != 2:
        raise ValueError(f"Expected 'module:variable' format, got '{module_app_str}'")
    module_name, var_name = parts
    module = importlib.import_module(module_name)
    return getattr(module, var_name)


def _status_phrase(code):
    try:
        return HTTPStatus(code).phrase
    except ValueError:
        return "Unknown"


def _parse_host_header(host_str, default_port=80):
    """Extract host and port from a Host header value."""
    if not host_str:
        return "localhost", default_port
    if host_str.startswith("["):
        bracket_end = host_str.find("]")
        if bracket_end != -1:
            host = host_str[1:bracket_end]
            rest = host_str[bracket_end + 1 :]
            if rest.startswith(":"):
                try:
                    return host, int(rest[1:])
                except ValueError:
                    return host, default_port
            return host, default_port
    if ":" in host_str:
        parts = host_str.rsplit(":", 1)
        try:
            return parts[0], int(parts[1])
        except ValueError:
            return host_str, default_port
    return host_str, default_port


# ==================== Performance helpers ====================

_STATUS_LINE_CACHE = {}


def _get_status_line(code):
    """Get pre-encoded HTTP/1.1 status line bytes, with caching."""
    line = _STATUS_LINE_CACHE.get(code)
    if line is None:
        phrase = _status_phrase(code)
        line = f"HTTP/1.1 {code} {phrase}\r\n".encode("latin-1")
        _STATUS_LINE_CACHE[code] = line
    return line


def _split_path(path):
    """Fast path/query split — replaces urlparse for request paths."""
    qmark = path.find("?")
    if qmark >= 0:
        return path[:qmark], path[qmark + 1 :]
    return path, ""


def _forwarded_scheme(raw_headers):
    """Original request scheme from X-Forwarded-Proto.

    The Go side always sets X-Forwarded-Proto on the hop to the worker
    (stripping any client-supplied value), so this value is trusted.
    """
    xfp = raw_headers.get("x-forwarded-proto", "").lower().strip()
    if xfp in ("https", "http"):
        return xfp
    return "http"


# Bounded memo caches for per-request parsing. Header names, Host values, and
# client addresses repeat across requests, so decode/validate work is cached.
# Size caps stop adversarial unique values from growing the dicts unboundedly;
# benign dict races under threads/greenlets only cause a redundant computation.
_HEADER_NAME_CACHE = {}
_HEADER_NAME_CACHE_MAX = 512

_HOST_HEADER_CACHE = {}
_HOST_HEADER_CACHE_MAX = 256

_CLIENT_IP_CACHE = {}
_CLIENT_IP_CACHE_MAX = 1024

_HTTP_VERSION_MAP = {
    "HTTP/1.1": "1.1",
    "HTTP/1.0": "1.0",
    "HTTP/2": "2",
    "HTTP/2.0": "2.0",
}


def _header_name_str(name_bytes):
    """Decode a header name to a lowercase str with memoization."""
    s = _HEADER_NAME_CACHE.get(name_bytes)
    if s is None:
        s = name_bytes.decode("latin-1")
        if not s.islower():
            s = s.lower()
        if len(_HEADER_NAME_CACHE) < _HEADER_NAME_CACHE_MAX:
            _HEADER_NAME_CACHE[bytes(name_bytes)] = s
    return s


def _parse_host_header_cached(host_str, default_port=80):
    """Memoized _parse_host_header for the default-port hot path."""
    if default_port != 80:
        return _parse_host_header(host_str, default_port)
    cached = _HOST_HEADER_CACHE.get(host_str)
    if cached is None:
        cached = _parse_host_header(host_str, default_port)
        if len(_HOST_HEADER_CACHE) < _HOST_HEADER_CACHE_MAX:
            _HOST_HEADER_CACHE[host_str] = cached
    return cached


def _validate_client_ip(addr):
    """Validate/normalize a client IP string with memoization. Returns str or None."""
    cached = _CLIENT_IP_CACHE.get(addr)
    if cached is None:
        host = addr.strip()
        if host.startswith("[") and host.endswith("]"):
            host = host[1:-1]
        try:
            cached = str(ipaddress.ip_address(host))
        except ValueError:
            cached = False
        if len(_CLIENT_IP_CACHE) < _CLIENT_IP_CACHE_MAX:
            _CLIENT_IP_CACHE[addr] = cached
    return cached or None


def _http_version_str(version):
    """Map a request-line HTTP version (e.g. "HTTP/1.1") to its ASGI/ESGI form."""
    v = _HTTP_VERSION_MAP.get(version)
    if v is not None:
        return v
    return version.split("/", 1)[1] if "/" in version else "1.1"


_wsgi_executor = concurrent.futures.ThreadPoolExecutor(
    max_workers=min(128, (os.cpu_count() or 1) * 8 + 16)
)

_ASGI_VERSION = {"version": "3.0", "spec_version": "2.3"}

_DRAIN_HIGH_WATER = 64 * 1024

MAX_REQUEST_LINE = 8192
MAX_HEADERS_SIZE = 64 * 1024  # 64 KB total header block
MAX_BODY_SIZE = 128 * 1024 * 1024  # 128 MB (internal IPC; external limits enforced by Caddy)
MAX_HEADER_COUNT = 100
MAX_WS_FRAME_SIZE = 16 * 1024 * 1024  # 16 MB
MAX_WS_MESSAGE_SIZE = 16 * 1024 * 1024  # 16 MB reassembled message


class _HttpBodyStream:
    """Incremental HTTP request body reader for fixed-length and chunked bodies."""

    def __init__(self, reader, content_length=None, chunked=False):
        self._reader = reader
        self._remaining = content_length
        self._chunked = chunked
        self._chunk_remaining = 0
        self._total_read = 0
        self._done = (content_length == 0) and not chunked

    def _bump_total(self, size):
        if size <= 0:
            return
        self._total_read += size
        if self._total_read > MAX_BODY_SIZE:
            raise ValueError("HTTP request body too large")

    async def read(self, max_bytes=64 * 1024):
        """Read up to max_bytes from the request body. Returns b"" on EOF."""
        if self._done:
            return b""
        if max_bytes is None or max_bytes <= 0:
            max_bytes = 64 * 1024

        if self._remaining is not None:
            if self._remaining <= 0:
                self._done = True
                return b""
            to_read = min(max_bytes, self._remaining)
            data = await self._reader.readexactly(to_read)
            self._bump_total(len(data))
            self._remaining -= len(data)
            if self._remaining == 0:
                self._done = True
            return data

        if not self._chunked:
            self._done = True
            return b""

        while True:
            if self._chunk_remaining == 0:
                size_line = await self._reader.readline()
                if not size_line:
                    raise asyncio.IncompleteReadError(size_line, 1)
                if len(size_line) > MAX_REQUEST_LINE:
                    raise ValueError("Chunk size line too long")
                size_str = size_line.strip()
                if b";" in size_str:
                    size_str = size_str.split(b";", 1)[0]
                try:
                    chunk_size = int(size_str, 16)
                except ValueError as exc:
                    raise ValueError("Invalid chunk size") from exc

                if chunk_size < 0:
                    raise ValueError("Invalid negative chunk size")
                if chunk_size == 0:
                    while True:
                        trailer_line = await self._reader.readline()
                        if trailer_line in (b"\r\n", b"\n", b""):
                            break
                        if len(trailer_line) > MAX_REQUEST_LINE:
                            raise ValueError("Chunk trailer line too long")
                    self._done = True
                    return b""
                self._chunk_remaining = chunk_size

            to_read = min(max_bytes, self._chunk_remaining)
            data = await self._reader.readexactly(to_read)
            self._bump_total(len(data))
            self._chunk_remaining -= len(data)
            if self._chunk_remaining == 0:
                ending = await self._reader.readexactly(2)
                if ending != b"\r\n":
                    raise ValueError("Invalid chunk terminator")
            if data:
                return data

    async def discard(self):
        """Drain any unread request body bytes so keep-alive stays in sync."""
        while True:
            chunk = await self.read()
            if not chunk:
                return


class _WsgiInputStream:
    """Blocking file-like adapter over an async body stream for WSGI apps."""

    def __init__(self, body_stream, loop):
        self._body_stream = body_stream
        self._loop = loop
        self._buffer = bytearray()
        self._eof = False

    def _fetch(self, max_bytes=64 * 1024):
        if self._eof:
            return b""
        fut = asyncio.run_coroutine_threadsafe(self._body_stream.read(max_bytes), self._loop)
        data = fut.result()
        if not data:
            self._eof = True
        return data

    def read(self, size=-1):
        if size is None or size < 0:
            while not self._eof:
                self._buffer.extend(self._fetch())
            out = bytes(self._buffer)
            self._buffer.clear()
            return out
        if size == 0:
            return b""
        while len(self._buffer) < size and not self._eof:
            self._buffer.extend(self._fetch())
        out = bytes(self._buffer[:size])
        del self._buffer[:size]
        return out

    def readline(self, size=-1):
        if size == 0:
            return b""
        limit = size if size is not None and size >= 0 else None
        while True:
            search_end = len(self._buffer) if limit is None else min(limit, len(self._buffer))
            newline = self._buffer.find(b"\n", 0, search_end)
            if newline != -1:
                end = newline + 1
                out = bytes(self._buffer[:end])
                del self._buffer[:end]
                return out
            if limit is not None and len(self._buffer) >= limit:
                out = bytes(self._buffer[:limit])
                del self._buffer[:limit]
                return out
            if self._eof:
                out = bytes(self._buffer)
                self._buffer.clear()
                return out
            self._buffer.extend(self._fetch())

    def readlines(self, hint=-1):
        lines = []
        total = 0
        while True:
            line = self.readline()
            if not line:
                break
            lines.append(line)
            total += len(line)
            if hint and hint > 0 and total >= hint:
                break
        return lines

    def __iter__(self):
        return self

    def __next__(self):
        line = self.readline()
        if not line:
            raise StopIteration
        return line


# ==================== WSGI Server ====================

# Windows does not support Unix domain sockets (AF_UNIX). Use TCP there.
_USE_TCP = sys.platform == "win32"


def _call_wsgi_app(app, environ):
    """Call a WSGI app synchronously. Returns (status_code, headers_list, body_bytes)."""
    response_started = []

    def start_response(status, response_headers, exc_info=None):
        if exc_info:
            try:
                if response_started:
                    raise exc_info[1].with_traceback(exc_info[2])
            finally:
                exc_info = None
        response_started.append((status, response_headers))
        return lambda s: None

    result = None
    try:
        result = app(environ, start_response)
        output = []
        for chunk in result:
            if chunk:
                output.append(chunk)
        if not response_started:
            return 500, [("Content-Type", "text/plain")], b"Internal Server Error"
        status_str, response_headers = response_started[0]
        status_code = int(status_str.split(" ", 1)[0])
        body = b"".join(output)
        return status_code, response_headers, body
    except Exception:
        traceback.print_exc(file=sys.stderr)
        return 500, [("Content-Type", "text/plain")], b"Internal Server Error"
    finally:
        if result is not None and hasattr(result, "close"):
            result.close()


def _client_from_caddy_snake_headers(raw_headers):
    """Parse trusted Caddy-Snake-Remote-* set by the Go worker. Returns (ip_str, port) or None."""
    addr = raw_headers.get("caddy-snake-remote-addr")
    if not addr:
        return None
    port_s = raw_headers.get("caddy-snake-remote-port", "0")
    try:
        port = int(port_s)
        if port < 0 or port > 65535:
            return None
    except ValueError:
        return None
    ip = _validate_client_ip(addr)
    if ip is None:
        return None
    return (ip, port)


def _build_wsgi_environ(method, path, version, headers_list, raw_headers, wsgi_input):
    """Build a WSGI environ dict from parsed HTTP request components."""
    path_part, query_string = _split_path(path)
    host_str = raw_headers.get("host", "localhost")
    server_name, server_port = _parse_host_header(host_str)

    remote_ip = "127.0.0.1"
    trusted_client = _client_from_caddy_snake_headers(raw_headers)
    if trusted_client is not None:
        remote_ip = trusted_client[0]

    environ = {
        "REQUEST_METHOD": method,
        "SCRIPT_NAME": "",
        "PATH_INFO": unquote(path_part) if "%" in path_part else path_part,
        "QUERY_STRING": query_string,
        "SERVER_NAME": server_name,
        "SERVER_PORT": str(server_port),
        "SERVER_PROTOCOL": version,
        "REMOTE_ADDR": remote_ip,
        "REMOTE_HOST": remote_ip,
        "X_FROM": "caddy-snake",
        "wsgi.version": (1, 0),
        "wsgi.url_scheme": _forwarded_scheme(raw_headers),
        "wsgi.input": wsgi_input,
        "wsgi.errors": sys.stderr,
        "wsgi.multithread": True,
        "wsgi.multiprocess": True,
        "wsgi.run_once": False,
    }

    content_type = raw_headers.get("content-type")
    if content_type:
        environ["CONTENT_TYPE"] = content_type
    content_length_val = raw_headers.get("content-length")
    if content_length_val:
        environ["CONTENT_LENGTH"] = content_length_val

    header_groups = {}
    for h_name_bytes, h_value_bytes in headers_list:
        key = h_name_bytes.decode("latin-1")
        key_upper = key.upper().replace("-", "_")
        if key_upper in (
            "CONTENT_TYPE",
            "CONTENT_LENGTH",
            "PROXY",
            "CADDY_SNAKE_REMOTE_ADDR",
            "CADDY_SNAKE_REMOTE_PORT",
        ):
            continue
        http_key = "HTTP_" + key_upper
        if http_key not in header_groups:
            header_groups[http_key] = []
        header_groups[http_key].append(h_value_bytes.decode("latin-1"))

    for http_key, values in header_groups.items():
        key_upper = http_key[5:]  # Remove "HTTP_" prefix
        if key_upper == "COOKIE":
            environ[http_key] = "; ".join(values)
        else:
            environ[http_key] = ", ".join(values)

    return environ


async def _handle_wsgi_connection(reader, writer, app):
    """Handle a single connection for WSGI (supports HTTP/1.1 keep-alive)."""
    loop = asyncio.get_running_loop()
    try:
        while True:
            result = await _read_http_request(reader)
            if result is None:
                break

            method, path, version, headers_list, raw_headers, body_stream = result
            wsgi_input = _WsgiInputStream(body_stream, loop)
            environ = _build_wsgi_environ(
                method, path, version, headers_list, raw_headers, wsgi_input
            )

            status_code, resp_headers, resp_body = await loop.run_in_executor(
                _wsgi_executor, _call_wsgi_app, app, environ
            )

            await body_stream.discard()

            buf = bytearray()
            buf.extend(_get_status_line(status_code))

            has_content_length = False
            for name, value in resp_headers:
                if isinstance(name, bytes):
                    name = name.decode("latin-1")
                if isinstance(value, bytes):
                    value = value.decode("latin-1")
                buf.extend(f"{name}: {value}\r\n".encode("latin-1"))
                if name.lower() == "content-length":
                    has_content_length = True

            if not has_content_length:
                buf.extend(b"Content-Length: ")
                buf.extend(str(len(resp_body)).encode("latin-1"))
                buf.extend(b"\r\n")

            buf.extend(b"\r\n")
            if resp_body:
                buf.extend(resp_body)
            writer.write(buf)
            try:
                if writer.transport.get_write_buffer_size() > _DRAIN_HIGH_WATER:
                    await writer.drain()
            except (AttributeError, TypeError):
                await writer.drain()

            connection = raw_headers.get("connection", "")
            if "close" in connection.lower():
                break
    except (
        asyncio.IncompleteReadError,
        ConnectionError,
        ConnectionResetError,
        OSError,
    ):
        pass
    finally:
        try:
            writer.close()
            await writer.wait_closed()
        except Exception:
            pass


async def _run_wsgi_server_async(app, socket_path):
    """Run a WSGI server using asyncio."""
    if _USE_TCP:
        server = await asyncio.start_server(
            lambda r, w: _handle_wsgi_connection(r, w, app),
            "127.0.0.1",
            0,
        )
        port = server.sockets[0].getsockname()[1]
        try:
            _write_port_file_atomic(socket_path, port)
        except OSError as e:
            print(f"Failed to write port file: {e}", file=sys.stderr)
            sys.exit(1)
        print(f"WSGI server listening on 127.0.0.1:{port}", file=sys.stderr)
    else:
        if os.path.exists(socket_path):
            os.unlink(socket_path)
        server = await asyncio.start_unix_server(
            lambda r, w: _handle_wsgi_connection(r, w, app),
            path=socket_path,
        )
        print(f"WSGI server listening on {socket_path}", file=sys.stderr)

    stop_event = asyncio.Event()
    loop = asyncio.get_running_loop()
    if sys.platform == "win32":

        def _stop(*args):
            stop_event.set()

        signal.signal(signal.SIGTERM, _stop)
        signal.signal(signal.SIGINT, _stop)
    else:
        try:
            for sig in (signal.SIGTERM, signal.SIGINT):
                loop.add_signal_handler(sig, stop_event.set)
        except (ValueError, OSError):

            def _stop(*args):
                stop_event.set()

            signal.signal(signal.SIGTERM, _stop)
            signal.signal(signal.SIGINT, _stop)

    await stop_event.wait()

    server.close()
    await server.wait_closed()

    sys.stderr.flush()
    sys.stdout.flush()

    try:
        if os.path.exists(socket_path):
            os.unlink(socket_path)
    except OSError:
        pass


def run_wsgi_server(app, socket_path, runtime: str = "sync"):
    """Run a WSGI server. On Unix: Unix socket at socket_path. On Windows: TCP, write port to socket_path."""
    if runtime == "gevent":
        print(
            "Note: runtime=gevent uses the same thread-pool scheduling as sync until "
            "native gevent integration lands.",
            file=sys.stderr,
        )
    asyncio.run(_run_wsgi_server_async(app, socket_path))


# ==================== ASGI Server ====================

WS_OPCODE_CONTINUATION = 0x0
WS_OPCODE_TEXT = 0x1
WS_OPCODE_BINARY = 0x2
WS_OPCODE_CLOSE = 0x8
WS_OPCODE_PING = 0x9
WS_OPCODE_PONG = 0xA
WS_MAGIC = b"258EAFA5-E914-47DA-95CA-C5AB0DC85B11"


async def ws_read_frame(reader):
    """Read and decode a single WebSocket frame."""
    data = await reader.readexactly(2)
    fin = (data[0] >> 7) & 1
    opcode = data[0] & 0xF
    masked = (data[1] >> 7) & 1
    payload_len = data[1] & 0x7F

    if payload_len == 126:
        data = await reader.readexactly(2)
        payload_len = struct.unpack("!H", data)[0]
    elif payload_len == 127:
        data = await reader.readexactly(8)
        payload_len = struct.unpack("!Q", data)[0]

    if payload_len > MAX_WS_FRAME_SIZE:
        raise ValueError(f"WebSocket frame too large: {payload_len}")

    mask_key = None
    if masked:
        mask_key = await reader.readexactly(4)

    payload = await reader.readexactly(payload_len) if payload_len > 0 else b""

    if mask_key:
        payload = bytes(b ^ mask_key[i % 4] for i, b in enumerate(payload))

    return fin, opcode, payload


def ws_build_frame(opcode, payload, fin=True):
    """Build a WebSocket frame (server-side, unmasked)."""
    frame = bytearray()
    frame.append(((1 if fin else 0) << 7) | opcode)

    length = len(payload)
    if length < 126:
        frame.append(length)
    elif length < 65536:
        frame.append(126)
        frame.extend(struct.pack("!H", length))
    else:
        frame.append(127)
        frame.extend(struct.pack("!Q", length))

    frame.extend(payload)
    return bytes(frame)


def ws_accept_key(key):
    """Compute the Sec-WebSocket-Accept value."""
    return base64.b64encode(hashlib.sha1(key.encode() + WS_MAGIC).digest()).decode()


def _write_port_file_atomic(path, value):
    """Atomically write port discovery file to avoid partial reads."""
    directory = os.path.dirname(path) or "."
    prefix = f".{os.path.basename(path)}."
    fd, tmp_path = tempfile.mkstemp(prefix=prefix, dir=directory)
    try:
        with os.fdopen(fd, "w") as f:
            f.write(str(value))
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp_path, path)
    finally:
        with contextlib.suppress(OSError):
            os.unlink(tmp_path)


async def _read_http_request(reader):
    """Parse an HTTP/1.1 request from an asyncio reader.

    Returns None on EOF, or (method, path, version, headers, raw_headers, body_stream).
    headers is a list of (name_bytes, value_bytes) for ASGI scope.
    raw_headers is a dict of lowercase-name -> value for internal use.
    """
    try:
        header_data = await reader.readuntil(b"\r\n\r\n")
    except (asyncio.IncompleteReadError, asyncio.LimitOverrunError):
        return None

    if len(header_data) > MAX_HEADERS_SIZE:
        return None

    lines = header_data[:-4].split(b"\r\n")
    if not lines:
        return None

    if len(lines[0]) > MAX_REQUEST_LINE:
        return None

    request_line = lines[0].decode("latin-1")
    parts = request_line.split(" ", 2)
    if len(parts) != 3:
        return None

    method, path, version = parts

    if len(lines) - 1 > MAX_HEADER_COUNT:
        return None

    headers = []
    raw_headers = {}
    for line in lines[1:]:
        colon = line.find(b":")
        if colon < 0:
            continue
        name = line[:colon].strip().lower()
        value = line[colon + 1 :].strip()
        headers.append((name, value))
        raw_headers[_header_name_str(name)] = value.decode("latin-1")

    body_stream = _HttpBodyStream(reader, content_length=0)
    content_length = raw_headers.get("content-length")
    transfer_encoding = raw_headers.get("transfer-encoding", "")

    # Reject ambiguous CL+TE requests (RFC 7230 §3.3.3)
    if content_length and transfer_encoding:
        return None

    if content_length:
        try:
            cl = int(content_length)
        except ValueError:
            return None
        if cl < 0 or cl > MAX_BODY_SIZE:
            return None
        body_stream = _HttpBodyStream(reader, content_length=cl)
    elif "chunked" in transfer_encoding.lower():
        body_stream = _HttpBodyStream(reader, chunked=True)

    return method, path, version, headers, raw_headers, body_stream


def _encode_header(name, value):
    """Ensure header name and value are bytes."""
    if isinstance(name, str):
        name = name.encode("latin-1")
    if isinstance(value, str):
        value = value.encode("latin-1")
    return name, value


async def _handle_asgi_http(writer, app, scope, body_stream):
    """Handle an ASGI HTTP connection."""
    body_done = False
    disconnect_event = asyncio.Event()

    async def receive():
        nonlocal body_done
        if not body_done:
            chunk = await body_stream.read()
            if chunk:
                return {"type": "http.request", "body": chunk, "more_body": True}
            body_done = True
            return {"type": "http.request", "body": b"", "more_body": False}
        await disconnect_event.wait()
        return {"type": "http.disconnect"}

    response_started = False
    response_complete = False
    use_chunked = False
    pending_headers = None
    has_cl = False
    has_te = False

    def _client_gone():
        # On native asyncio a failed write does not raise; the transport is
        # force-closed and is_closing() flips to True. uvloop raises directly
        # (handled by _CLIENT_DISCONNECT_ERRORS), but is_closing() is a cheap
        # check that stops a streaming app promptly on either runtime without
        # consuming bytes from the socket (which would corrupt keep-alive).
        is_closing = getattr(writer, "is_closing", None)
        return bool(is_closing()) if callable(is_closing) else False

    def _write_to_client(data):
        try:
            writer.write(data)
        except _CLIENT_DISCONNECT_ERRORS:
            disconnect_event.set()
            raise ClientDisconnected from None

    async def _drain_to_client():
        try:
            await writer.drain()
        except _CLIENT_DISCONNECT_ERRORS:
            disconnect_event.set()
            raise ClientDisconnected from None

    async def _drain_if_needed():
        # Drain only above the high-water mark (same policy as the WSGI
        # handler): a no-op drain still costs a coroutine round per request,
        # and below the mark the transport buffer is bounded anyway.
        try:
            if writer.transport.get_write_buffer_size() <= _DRAIN_HIGH_WATER:
                return
        except (AttributeError, TypeError):
            pass
        await _drain_to_client()

    async def send(message):
        nonlocal response_started, response_complete, use_chunked
        nonlocal pending_headers, has_cl, has_te
        if disconnect_event.is_set() or _client_gone():
            disconnect_event.set()
            raise ClientDisconnected from None
        msg_type = message["type"]

        if msg_type == "http.response.start":
            response_started = True
            status = message["status"]
            resp_headers = message.get("headers", [])

            buf = bytearray()
            buf.extend(_get_status_line(status))
            for h_name, h_value in resp_headers:
                if isinstance(h_name, str):
                    h_name = h_name.encode("latin-1")
                if isinstance(h_value, str):
                    h_value = h_value.encode("latin-1")
                h_lower = h_name.lower()
                if h_lower == b"content-length":
                    has_cl = True
                elif h_lower == b"transfer-encoding":
                    has_te = True
                buf.extend(h_name)
                buf.extend(b": ")
                buf.extend(h_value)
                buf.extend(b"\r\n")

            pending_headers = buf

        elif msg_type == "http.response.body":
            body_data = message.get("body", b"")
            more_body = message.get("more_body", False)

            if pending_headers is not None:
                buf = pending_headers
                pending_headers = None
                if not has_cl and not has_te:
                    if not more_body:
                        buf.extend(b"Content-Length: ")
                        buf.extend(str(len(body_data)).encode("latin-1"))
                        buf.extend(b"\r\n")
                    else:
                        use_chunked = True
                        buf.extend(b"transfer-encoding: chunked\r\n")
                buf.extend(b"\r\n")
                if use_chunked:
                    if body_data:
                        buf.extend(f"{len(body_data):x}\r\n".encode("ascii"))
                        buf.extend(body_data)
                        buf.extend(b"\r\n")
                    if not more_body:
                        buf.extend(b"0\r\n\r\n")
                else:
                    if body_data:
                        buf.extend(body_data)
                _write_to_client(buf)
            else:
                if use_chunked:
                    if body_data:
                        _write_to_client(
                            f"{len(body_data):x}\r\n".encode("ascii") + body_data + b"\r\n"
                        )
                    if not more_body:
                        _write_to_client(b"0\r\n\r\n")
                else:
                    if body_data:
                        _write_to_client(body_data)

            if more_body:
                # Backpressure for streaming responses: bound the transport
                # buffer instead of letting a fast producer outrun a slow
                # client indefinitely.
                await _drain_if_needed()
            else:
                await _drain_if_needed()
                response_complete = True
                disconnect_event.set()

    async def _send_error_response():
        nonlocal pending_headers
        if disconnect_event.is_set():
            return
        try:
            if not response_started:
                _write_to_client(
                    b"HTTP/1.1 500 Internal Server Error\r\n"
                    b"Content-Length: 21\r\n\r\n"
                    b"Internal Server Error"
                )
                await _drain_to_client()
            else:
                if pending_headers is not None:
                    pending_headers.extend(b"\r\n")
                    _write_to_client(pending_headers)
                    pending_headers = None
                if use_chunked and not response_complete:
                    _write_to_client(b"0\r\n\r\n")
                await _drain_to_client()
        except ClientDisconnected:
            pass

    try:
        await app(scope, receive, send)
    except ClientDisconnected:
        pass
    except Exception:
        if not disconnect_event.is_set():
            traceback.print_exc(file=sys.stderr)
            await _send_error_response()
    finally:
        disconnect_event.set()
        if not body_stream._done:
            await body_stream.discard()


async def _ws_close_with_code(writer, code):
    """Send a websocket close frame with the provided status code."""
    payload = struct.pack("!H", code)
    frame = ws_build_frame(WS_OPCODE_CLOSE, payload)
    try:
        writer.write(frame)
        await writer.drain()
    except (ConnectionError, OSError):
        pass


async def _ws_read_loop(reader, receive_queue, closed_event, writer):
    """Read WebSocket frames and enqueue ASGI receive events."""
    fragment_opcode = None
    fragment_buffer = bytearray()

    async def protocol_error(code):
        await _ws_close_with_code(writer, code)
        await receive_queue.put({"type": "websocket.disconnect", "code": code})

    try:
        while not closed_event.is_set():
            fin, opcode, payload = await ws_read_frame(reader)

            is_control = opcode & 0x8
            if is_control and (not fin or len(payload) > 125):
                await protocol_error(1002)
                break

            if opcode == WS_OPCODE_CLOSE:
                code = 1005
                if len(payload) == 1:
                    code = 1002
                elif len(payload) >= 2:
                    code = struct.unpack("!H", payload[:2])[0]
                await receive_queue.put({"type": "websocket.disconnect", "code": code})
                break
            elif opcode == WS_OPCODE_PING:
                frame = ws_build_frame(WS_OPCODE_PONG, payload)
                writer.write(frame)
                await writer.drain()
            elif opcode == WS_OPCODE_PONG:
                continue
            elif opcode == WS_OPCODE_CONTINUATION:
                if fragment_opcode is None:
                    await protocol_error(1002)
                    break

                fragment_buffer.extend(payload)
                if len(fragment_buffer) > MAX_WS_MESSAGE_SIZE:
                    await protocol_error(1009)
                    break

                if fin:
                    if fragment_opcode == WS_OPCODE_TEXT:
                        try:
                            message = fragment_buffer.decode("utf-8")
                        except UnicodeDecodeError:
                            await protocol_error(1007)
                            break
                        await receive_queue.put({"type": "websocket.receive", "text": message})
                    else:
                        await receive_queue.put(
                            {
                                "type": "websocket.receive",
                                "bytes": bytes(fragment_buffer),
                            }
                        )
                    fragment_opcode = None
                    fragment_buffer.clear()
            elif opcode in (WS_OPCODE_TEXT, WS_OPCODE_BINARY):
                if fragment_opcode is not None:
                    await protocol_error(1002)
                    break

                if fin:
                    if opcode == WS_OPCODE_TEXT:
                        try:
                            message = payload.decode("utf-8")
                        except UnicodeDecodeError:
                            await protocol_error(1007)
                            break
                        await receive_queue.put({"type": "websocket.receive", "text": message})
                    else:
                        await receive_queue.put({"type": "websocket.receive", "bytes": payload})
                else:
                    fragment_opcode = opcode
                    fragment_buffer = bytearray(payload)
                    if len(fragment_buffer) > MAX_WS_MESSAGE_SIZE:
                        await protocol_error(1009)
                        break
            else:
                await protocol_error(1002)
                break
    except ValueError:
        await receive_queue.put({"type": "websocket.disconnect", "code": 1002})
    except (asyncio.IncompleteReadError, ConnectionError, OSError):
        await receive_queue.put({"type": "websocket.disconnect", "code": 1006})


async def _handle_asgi_websocket(reader, writer, app, scope, raw_headers):
    """Handle an ASGI WebSocket connection."""
    ws_key = raw_headers.get("sec-websocket-key", "")

    receive_queue = asyncio.Queue()
    await receive_queue.put({"type": "websocket.connect"})

    ws_accepted = asyncio.Event()
    ws_closed = asyncio.Event()
    read_task = None

    async def receive():
        return await receive_queue.get()

    async def send(message):
        nonlocal read_task
        msg_type = message["type"]

        if msg_type == "websocket.accept":
            accept_value = ws_accept_key(ws_key)
            response = (
                "HTTP/1.1 101 Switching Protocols\r\n"
                "Upgrade: websocket\r\n"
                "Connection: Upgrade\r\n"
                f"Sec-WebSocket-Accept: {accept_value}\r\n"
            )
            subprotocol = message.get("subprotocol")
            if subprotocol:
                response += f"Sec-WebSocket-Protocol: {subprotocol}\r\n"
            for h_name, h_value in message.get("headers", []):
                if isinstance(h_name, bytes):
                    h_name = h_name.decode("latin-1")
                if isinstance(h_value, bytes):
                    h_value = h_value.decode("latin-1")
                response += f"{h_name}: {h_value}\r\n"
            response += "\r\n"
            writer.write(response.encode("latin-1"))
            await writer.drain()
            ws_accepted.set()
            read_task = asyncio.create_task(_ws_read_loop(reader, receive_queue, ws_closed, writer))

        elif msg_type == "websocket.send":
            if "text" in message:
                frame = ws_build_frame(WS_OPCODE_TEXT, message["text"].encode("utf-8"))
            elif "bytes" in message:
                frame = ws_build_frame(WS_OPCODE_BINARY, message["bytes"])
            else:
                return
            writer.write(frame)
            await writer.drain()

        elif msg_type == "websocket.close":
            if not ws_accepted.is_set():
                # ASGI app rejected connection before accept: respond with HTTP 403
                try:
                    writer.write(
                        b"HTTP/1.1 403 Forbidden\r\nContent-Length: 13\r\n\r\n403 Forbidden"
                    )
                    await writer.drain()
                except (ConnectionError, OSError):
                    pass
            else:
                code = message.get("code", 1000)
                reason = message.get("reason", "")
                payload = struct.pack("!H", code) + reason.encode("utf-8")
                frame = ws_build_frame(WS_OPCODE_CLOSE, payload)
                try:
                    writer.write(frame)
                    await writer.drain()
                except (ConnectionError, OSError):
                    pass
            ws_closed.set()

    try:
        await app(scope, receive, send)
    except Exception:
        traceback.print_exc(file=sys.stderr)
        if not ws_accepted.is_set():
            writer.write(
                b"HTTP/1.1 500 Internal Server Error\r\n"
                b"Content-Length: 21\r\n\r\n"
                b"Internal Server Error"
            )
            await writer.drain()
    finally:
        ws_closed.set()
        if read_task and not read_task.done():
            read_task.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await read_task


# ==================== ESGI Server (gevent, 0.1-draft) ====================
# Specification: https://github.com/mliezun/esgi/blob/main/PROTOCOL.md
#
# ESGI is gevent-only: StreamServer + plain sockets + greenlets. No asyncio in this
# worker mode. Each client connection runs in its own greenlet; apps are synchronous.


def _configure_asgi_event_loop(runtime: str) -> None:
    """Apply event loop policy before asyncio.run for ASGI workers."""
    if runtime in ("uvloop", "libuv"):
        try:
            import uvloop

            asyncio.set_event_loop_policy(uvloop.EventLoopPolicy())
        except ImportError:
            print(
                "warning: ASGI runtime is uvloop but uvloop is not installed; "
                "falling back to native asyncio",
                file=sys.stderr,
            )
    # native: standard asyncio loop


def _recv_exact_sock(sock, n: int) -> bytes:
    chunks = []
    got = 0
    while got < n:
        b = sock.recv(n - got)
        if not b:
            raise OSError("connection closed while reading HTTP/WebSocket")
        chunks.append(b)
        got += len(b)
    return b"".join(chunks)


def _read_http_request_sync(sock):
    """Blocking HTTP/1.x request parser; returns None on EOF/parse error."""
    buf = bytearray()
    search_from = 0
    while True:
        if len(buf) > MAX_HEADERS_SIZE:
            return None
        sep = buf.find(b"\r\n\r\n", search_from)
        if sep >= 0:
            lines = bytes(buf[:sep]).split(b"\r\n")
            leftover = bytes(buf[sep + 4 :])
            break
        # The separator can straddle a recv boundary; back up 3 bytes.
        search_from = max(0, len(buf) - 3)
        chunk = sock.recv(16384)
        if not chunk:
            return None
        buf.extend(chunk)

    if not lines or len(lines[0]) > MAX_REQUEST_LINE:
        return None
    request_line = lines[0].decode("latin-1")
    parts = request_line.split(" ", 2)
    if len(parts) != 3:
        return None
    method, path, version = parts

    if len(lines) - 1 > MAX_HEADER_COUNT:
        return None

    headers = []
    raw_headers = {}
    for line in lines[1:]:
        colon = line.find(b":")
        if colon < 0:
            continue
        name = line[:colon].strip().lower()
        value = line[colon + 1 :].strip()
        headers.append((name, value))
        raw_headers[_header_name_str(name)] = value.decode("latin-1")

    content_length = raw_headers.get("content-length")
    transfer_encoding = raw_headers.get("transfer-encoding", "")
    if content_length and transfer_encoding:
        return None

    if content_length:
        try:
            cl = int(content_length)
        except ValueError:
            return None
        if cl < 0 or cl > MAX_BODY_SIZE:
            return None
        body_stream = _SyncHttpBodyStream(sock, leftover, content_length=cl)
    elif "chunked" in transfer_encoding.lower():
        body_stream = _SyncHttpBodyStream(sock, leftover, chunked=True)
    else:
        body_stream = _SyncHttpBodyStream(sock, leftover, content_length=0)

    return method, path, version, headers, raw_headers, body_stream


class _SyncHttpBodyStream:
    """Blocking request body reader (Content-Length or chunked)."""

    def __init__(self, sock, initial=b"", content_length=None, chunked=False):
        self._sock = sock
        self._buf = bytearray(initial)
        self._remaining = content_length
        self._chunked = chunked
        self._chunk_remaining = 0
        self._total_read = 0
        self._done = (content_length == 0) and not chunked

    def _bump_total(self, size):
        if size <= 0:
            return
        self._total_read += size
        if self._total_read > MAX_BODY_SIZE:
            raise ValueError("HTTP request body too large")

    def read(self, max_bytes=64 * 1024):
        if self._done:
            return b""
        if max_bytes is None or max_bytes <= 0:
            max_bytes = 64 * 1024

        if self._remaining is not None:
            if self._remaining <= 0:
                self._done = True
                return b""
            to_read = min(max_bytes, self._remaining)
            if len(self._buf) < to_read:
                need = to_read - len(self._buf)
                chunk = self._sock.recv(max(need, 4096))
                if not chunk:
                    raise OSError("unexpected EOF reading body")
                self._buf.extend(chunk)
            data = bytes(self._buf[:to_read])
            del self._buf[:to_read]
            self._bump_total(len(data))
            self._remaining -= len(data)
            if self._remaining == 0:
                self._done = True
            return data

        if not self._chunked:
            self._done = True
            return b""

        while True:
            if self._chunk_remaining == 0:
                line = self._readline_chunked()
                if not line:
                    raise OSError("EOF in chunked body")
                if len(line) > MAX_REQUEST_LINE:
                    raise ValueError("Chunk size line too long")
                size_str = line.strip()
                if b";" in size_str:
                    size_str = size_str.split(b";", 1)[0]
                try:
                    chunk_size = int(size_str, 16)
                except ValueError as exc:
                    raise ValueError("Invalid chunk size") from exc
                if chunk_size < 0:
                    raise ValueError("Invalid negative chunk size")
                if chunk_size == 0:
                    while True:
                        trailer_line = self._readline_chunked()
                        if trailer_line in (b"\r\n", b"\n", b""):
                            break
                        if len(trailer_line) > MAX_REQUEST_LINE:
                            raise ValueError("Chunk trailer line too long")
                    self._done = True
                    return b""
                self._chunk_remaining = chunk_size

            to_read = min(max_bytes, self._chunk_remaining)
            if len(self._buf) < to_read:
                need = to_read - len(self._buf)
                chunk = self._sock.recv(max(need, 4096))
                if not chunk:
                    raise OSError("unexpected EOF in chunk")
                self._buf.extend(chunk)
            data = bytes(self._buf[:to_read])
            del self._buf[:to_read]
            self._bump_total(len(data))
            self._chunk_remaining -= len(data)
            if self._chunk_remaining == 0:
                _recv_exact_sock(self._sock, 2)
            if data:
                return data

    def _readline_chunked(self):
        while True:
            nl = self._buf.find(b"\n")
            if nl >= 0:
                line = bytes(self._buf[: nl + 1])
                del self._buf[: nl + 1]
                return line
            chunk = self._sock.recv(4096)
            if not chunk:
                return b""
            self._buf.extend(chunk)

    def discard(self):
        while True:
            chunk = self.read()
            if not chunk:
                return


def _esgi_http_version(req_version: str) -> str:
    return _http_version_str(req_version)


def _esgi_scheme_from_headers(raw_headers: dict) -> str:
    return _forwarded_scheme(raw_headers)


def _esgi_ws_scheme_from_headers(raw_headers: dict) -> str:
    return "wss" if _esgi_scheme_from_headers(raw_headers) == "https" else "ws"


def _esgi_headers_mapping(headers_list, raw_headers: dict) -> dict:
    headers_map = {}
    for n, v in headers_list:
        k = _header_name_str(n)
        if k in ("caddy-snake-remote-addr", "caddy-snake-remote-port"):
            continue
        val = v.decode("latin-1")
        existing = headers_map.get(k)
        if existing is not None:
            headers_map[k] = existing + ", " + val
        else:
            headers_map[k] = val
    return headers_map


def _esgi_decode_path(path_part: str) -> str:
    if "%" not in path_part:
        return path_part
    return unquote(path_part, encoding="utf-8", errors="replace")


def _build_esgi_http_scope(method, path, version, headers_list, raw_headers: dict):
    path_part, query_string = _split_path(path)
    path_decoded = _esgi_decode_path(path_part)
    host_str = raw_headers.get("host", "localhost")
    server_host, server_port = _parse_host_header_cached(host_str)
    trusted = _client_from_caddy_snake_headers(raw_headers)
    if trusted:
        client_host, client_port = trusted
    else:
        client_host, client_port = "127.0.0.1", 0

    return {
        "proto": "http",
        "esgi_version": "0.1",
        "http_version": _esgi_http_version(version),
        "server": f"{server_host}:{server_port}",
        "client": f"{client_host}:{client_port}",
        "scheme": _esgi_scheme_from_headers(raw_headers),
        "method": method.upper(),
        "path": path_decoded,
        "query_string": query_string,
        "headers": _esgi_headers_mapping(headers_list, raw_headers),
        "authority": raw_headers.get("host") or None,
    }


def _build_esgi_ws_scope(method, path, version, headers_list, raw_headers: dict):
    path_part, query_string = _split_path(path)
    path_decoded = _esgi_decode_path(path_part)
    host_str = raw_headers.get("host", "localhost")
    server_host, server_port = _parse_host_header_cached(host_str)
    trusted = _client_from_caddy_snake_headers(raw_headers)
    if trusted:
        client_host, client_port = trusted
    else:
        client_host, client_port = "127.0.0.1", 0

    return {
        "proto": "ws",
        "esgi_version": "0.1",
        "http_version": _esgi_http_version(version),
        "server": f"{server_host}:{server_port}",
        "client": f"{client_host}:{client_port}",
        "scheme": _esgi_ws_scheme_from_headers(raw_headers),
        "method": method.upper(),
        "path": path_decoded,
        "query_string": query_string,
        "headers": _esgi_headers_mapping(headers_list, raw_headers),
        "authority": raw_headers.get("host") or None,
    }


class _EsgiWsMessage:
    __slots__ = ("kind", "data")

    def __init__(self, kind, data=None):
        self.kind = kind
        self.data = data


def ws_read_frame_sync(sock):
    data = _recv_exact_sock(sock, 2)
    fin = (data[0] >> 7) & 1
    opcode = data[0] & 0xF
    masked = (data[1] >> 7) & 1
    payload_len = data[1] & 0x7F

    if payload_len == 126:
        data = _recv_exact_sock(sock, 2)
        payload_len = struct.unpack("!H", data)[0]
    elif payload_len == 127:
        data = _recv_exact_sock(sock, 8)
        payload_len = struct.unpack("!Q", data)[0]

    if payload_len > MAX_WS_FRAME_SIZE:
        raise ValueError(f"WebSocket frame too large: {payload_len}")

    mask_key = None
    if masked:
        mask_key = _recv_exact_sock(sock, 4)

    payload = _recv_exact_sock(sock, payload_len) if payload_len > 0 else b""

    if mask_key:
        payload = bytes(b ^ mask_key[i % 4] for i, b in enumerate(payload))

    return fin, opcode, payload


def _ws_close_frame_bytes(code: int) -> bytes:
    payload = struct.pack("!H", code)
    return ws_build_frame(WS_OPCODE_CLOSE, payload)


def _esgi_ws_reader_greenlet(sock, msg_queue):
    try:
        while True:
            fin, opcode, payload = ws_read_frame_sync(sock)
            if opcode == WS_OPCODE_CLOSE:
                code = 1005
                if len(payload) >= 2:
                    code = struct.unpack("!H", payload[:2])[0]
                msg_queue.put(_EsgiWsMessage(0, code))
                break
            if opcode == WS_OPCODE_PING:
                sock.sendall(ws_build_frame(WS_OPCODE_PONG, payload))
                continue
            if opcode == WS_OPCODE_PONG:
                continue
            if opcode == WS_OPCODE_TEXT:
                if not fin:
                    sock.sendall(_ws_close_frame_bytes(1003))
                    msg_queue.put(_EsgiWsMessage(0, 1003))
                    break
                try:
                    text = payload.decode("utf-8")
                except UnicodeDecodeError:
                    sock.sendall(_ws_close_frame_bytes(1007))
                    msg_queue.put(_EsgiWsMessage(0, 1007))
                    break
                msg_queue.put(_EsgiWsMessage(2, text))
            elif opcode == WS_OPCODE_BINARY:
                if not fin:
                    sock.sendall(_ws_close_frame_bytes(1003))
                    msg_queue.put(_EsgiWsMessage(0, 1003))
                    break
                msg_queue.put(_EsgiWsMessage(1, payload))
            else:
                sock.sendall(_ws_close_frame_bytes(1002))
                msg_queue.put(_EsgiWsMessage(0, 1002))
                break
    except (ValueError, OSError, ConnectionError):
        msg_queue.put(_EsgiWsMessage(0, 1006))


class _EsgiWsTransportSync:
    def __init__(self, sock, msg_queue):
        self._sock = sock
        self._q = msg_queue

    def receive(self):
        return self._q.get()

    def send_bytes(self, data: bytes) -> None:
        self._sock.sendall(ws_build_frame(WS_OPCODE_BINARY, data))

    def send_str(self, data: str) -> None:
        self._sock.sendall(ws_build_frame(WS_OPCODE_TEXT, data.encode("utf-8")))


class _EsgiWsRootProtocolSync:
    def __init__(self, sock, raw_headers):
        self._sock = sock
        self._raw_headers = raw_headers
        self._accepted = False
        self._closed = False
        self._reader = None
        self._q = None

    def accept(self):
        if self._accepted:
            raise RuntimeError("websocket already accepted")
        import gevent.queue

        ws_key = self._raw_headers.get("sec-websocket-key", "")
        if not ws_key:
            raise ValueError("missing Sec-WebSocket-Key")
        accept_value = ws_accept_key(ws_key)
        response = (
            "HTTP/1.1 101 Switching Protocols\r\n"
            "Upgrade: websocket\r\n"
            "Connection: Upgrade\r\n"
            f"Sec-WebSocket-Accept: {accept_value}\r\n\r\n"
        )
        self._sock.sendall(response.encode("latin-1"))
        self._q = gevent.queue.Queue()
        import gevent

        self._reader = gevent.spawn(_esgi_ws_reader_greenlet, self._sock, self._q)
        self._accepted = True
        return _EsgiWsTransportSync(self._sock, self._q)

    def close(self, code=None, reason=None) -> None:
        if self._closed:
            return
        self._closed = True
        if not self._accepted:
            with contextlib.suppress(OSError):
                self._sock.sendall(
                    b"HTTP/1.1 403 Forbidden\r\nContent-Length: 13\r\n\r\n403 Forbidden"
                )
            return
        c = 1000 if code is None else int(code)
        r = reason or ""
        payload = struct.pack("!H", c) + r.encode("utf-8")
        with contextlib.suppress(OSError):
            self._sock.sendall(ws_build_frame(WS_OPCODE_CLOSE, payload))
        if self._reader is not None:
            self._reader.kill(block=False)

    def cleanup_after_app(self):
        if self._reader is not None:
            try:
                if not self._reader.dead:
                    self._reader.kill(block=False)
            except Exception:
                pass


class _EsgiHttpProtocolSync:
    def __init__(self, sock, body_stream):
        self._sock = sock
        self._body_stream = body_stream
        self._responded = False
        self._body_started = False

    def _ensure_not_responded(self):
        if self._responded:
            raise RuntimeError("response already sent for this request")

    def read_body(self) -> bytes:
        if self._body_started:
            raise RuntimeError("request body already consumed")
        self._body_started = True
        chunks = []
        while True:
            chunk = self._body_stream.read()
            if not chunk:
                break
            chunks.append(chunk)
        return b"".join(chunks)

    def iter_body(self):
        if self._body_started:
            raise RuntimeError("request body already consumed")
        self._body_started = True

        def gen():
            while True:
                chunk = self._body_stream.read()
                if not chunk:
                    break
                yield chunk

        return gen()

    def _write_response(self, status, headers, body: bytes):
        cl_present = False
        parts = []
        for name, value in headers:
            parts.append(name)
            parts.append(": ")
            parts.append(value)
            parts.append("\r\n")
            if name.lower() == "content-length":
                cl_present = True
        buf = bytearray(_get_status_line(status))
        buf.extend("".join(parts).encode("latin-1"))
        if not cl_present:
            buf.extend(b"Content-Length: ")
            buf.extend(str(len(body)).encode("ascii"))
            buf.extend(b"\r\n")
        buf.extend(b"\r\n")
        buf.extend(body)
        # sendall accepts any buffer; avoid copying the full response again.
        self._sock.sendall(buf)

    def response_empty(self, status: int, headers: list) -> None:
        self.response_bytes(status, headers, b"")

    def response_str(self, status: int, headers: list, body: str) -> None:
        self.response_bytes(status, headers, body.encode("utf-8"))

    def response_bytes(self, status: int, headers: list, body: bytes) -> None:
        self._ensure_not_responded()
        self._responded = True
        self._write_response(status, headers, body)

    def response_file(self, status: int, headers: list, file) -> None:
        path = os.fsdecode(file)
        with open(path, "rb") as f:
            data = f.read()
        self.response_bytes(status, headers, data)

    def response_file_range(self, status: int, headers: list, file, start: int, end: int) -> None:
        path = os.fsdecode(file)
        with open(path, "rb") as f:
            f.seek(start)
            data = f.read(end - start)
        self.response_bytes(status, headers, data)

    def response_stream(self, status: int, headers: list):
        raise NotImplementedError("ESGI response_stream is not implemented yet")

    def wait_disconnect(self) -> None:
        pass


def _invoke_esgi_app(app, scope, protocol) -> None:
    if hasattr(app, "__esgi__"):
        app.__esgi__(scope, protocol)
    else:
        app(scope, protocol)


def _handle_esgi_http_sync(
    sock, app, method, path, version, headers_list, raw_headers, body_stream
):
    scope = _build_esgi_http_scope(method, path, version, headers_list, raw_headers)
    protocol = _EsgiHttpProtocolSync(sock, body_stream)
    try:
        _invoke_esgi_app(app, scope, protocol)
    except Exception:
        traceback.print_exc(file=sys.stderr)
        if not protocol._responded:
            with contextlib.suppress(Exception):
                protocol.response_bytes(
                    500,
                    [("Content-Type", "text/plain")],
                    b"Internal Server Error",
                )
    if not protocol._body_started:
        body_stream.discard()


def _handle_esgi_websocket_sync(sock, app, raw_headers, headers_list, version, path, method):
    scope = _build_esgi_ws_scope(method, path, version, headers_list, raw_headers)
    ws_proto = _EsgiWsRootProtocolSync(sock, raw_headers)
    try:
        _invoke_esgi_app(app, scope, ws_proto)
    except Exception:
        traceback.print_exc(file=sys.stderr)
        if not ws_proto._accepted:
            with contextlib.suppress(OSError):
                sock.sendall(
                    b"HTTP/1.1 500 Internal Server Error\r\n"
                    b"Content-Length: 21\r\n\r\n"
                    b"Internal Server Error"
                )
    finally:
        ws_proto.cleanup_after_app()


def _esgi_connection_loop(sock, app):
    try:
        while True:
            result = _read_http_request_sync(sock)
            if result is None:
                return
            method, path, version, headers_list, raw_headers, body_stream = result
            upgrade = raw_headers.get("upgrade")
            is_websocket = (
                upgrade is not None
                and method.upper() == "GET"
                and upgrade.lower() == "websocket"
                and "upgrade" in raw_headers.get("connection", "").lower()
            )
            if is_websocket:
                body_stream.discard()
                _handle_esgi_websocket_sync(
                    sock, app, raw_headers, headers_list, version, path, method
                )
                return
            _handle_esgi_http_sync(
                sock, app, method, path, version, headers_list, raw_headers, body_stream
            )
            connection = raw_headers.get("connection", "")
            if "close" in connection.lower():
                return
    except (OSError, ValueError, ConnectionError):
        pass


def run_esgi_server(app, socket_path: str, runtime: str):
    if runtime != "gevent":
        print("ESGI requires runtime gevent (no asyncio transport)", file=sys.stderr)
        sys.exit(1)

    try:
        import gevent
        import gevent.socket
        from gevent.server import StreamServer
    except ImportError:
        print("ESGI requires gevent; pip install gevent", file=sys.stderr)
        sys.exit(1)

    import gevent.signal as gsig

    if hasattr(app, "__esgi_init__"):
        try:
            app.__esgi_init__()
        except Exception:
            traceback.print_exc(file=sys.stderr)
            sys.exit(1)

    def handle(sock, _addr):
        if _USE_TCP:
            import socket as _stdsocket

            with contextlib.suppress(OSError):
                sock.setsockopt(_stdsocket.IPPROTO_TCP, _stdsocket.TCP_NODELAY, 1)
        try:
            _esgi_connection_loop(sock, app)
        finally:
            with contextlib.suppress(OSError):
                sock.close()

    server = None

    try:
        if _USE_TCP:
            server = StreamServer(("127.0.0.1", 0), handle)
        else:
            if os.path.exists(socket_path):
                os.unlink(socket_path)
            listener_sock = gevent.socket.socket(gevent.socket.AF_UNIX, gevent.socket.SOCK_STREAM)
            listener_sock.bind(socket_path)
            listener_sock.listen(256)
            server = StreamServer(listener_sock, handle)
        server.start()

        if _USE_TCP:
            port = server.socket.getsockname()[1]
            try:
                _write_port_file_atomic(socket_path, port)
            except OSError as e:
                print(f"Failed to write port file: {e}", file=sys.stderr)
                sys.exit(1)
            print(f"ESGI server listening on 127.0.0.1:{port}", file=sys.stderr)
        else:
            print(f"ESGI server listening on {socket_path}", file=sys.stderr)

        def _stop(*_args):
            if server is not None:
                with contextlib.suppress(Exception):
                    server.close()

        gsig.signal(signal.SIGTERM, _stop)
        gsig.signal(signal.SIGINT, _stop)
        server.serve_forever()
    finally:
        if hasattr(app, "__esgi_del__"):
            try:
                app.__esgi_del__()
            except Exception:
                traceback.print_exc(file=sys.stderr)
        if server is not None:
            with contextlib.suppress(Exception):
                server.close()
        try:
            if not _USE_TCP and os.path.exists(socket_path):
                os.unlink(socket_path)
        except OSError:
            pass
        sys.stderr.flush()
        sys.stdout.flush()


async def _handle_asgi_connection(reader, writer, app, state):
    """Handle a single TCP connection (supports keep-alive)."""
    try:
        while True:
            result = await _read_http_request(reader)
            if result is None:
                break

            method, path, version, headers, raw_headers, body_stream = result
            path_part, query_string = _split_path(path)

            upgrade = raw_headers.get("upgrade")
            is_websocket = (
                upgrade is not None
                and method == "GET"
                and upgrade.lower() == "websocket"
                and "upgrade" in raw_headers.get("connection", "").lower()
            )

            host_str = raw_headers.get("host", "localhost")
            server_host, server_port = _parse_host_header_cached(host_str)

            headers_for_scope = [
                (n, v)
                for n, v in headers
                if n not in (b"caddy-snake-remote-addr", b"caddy-snake-remote-port")
            ]
            trusted_client = _client_from_caddy_snake_headers(raw_headers)
            if trusted_client is not None:
                client_host, client_port = trusted_client
                client_tp = (client_host, client_port)
            else:
                client_tp = ("127.0.0.1", 0)

            scheme = _forwarded_scheme(raw_headers)
            if is_websocket:
                conn_type = "websocket"
                scheme = "wss" if scheme == "https" else "ws"
            else:
                conn_type = "http"

            scope = {
                "type": conn_type,
                "asgi": _ASGI_VERSION,
                "http_version": _http_version_str(version),
                "method": method,
                "path": unquote(path_part) if "%" in path_part else path_part,
                "raw_path": path_part.encode("latin-1"),
                "query_string": query_string.encode("latin-1"),
                "root_path": "",
                "scheme": scheme,
                "headers": headers_for_scope,
                "server": (server_host, server_port),
                "client": client_tp,
            }

            if state is not None:
                scope["state"] = dict(state)

            if is_websocket:
                subprotocols_str = raw_headers.get("sec-websocket-protocol", "")
                scope["subprotocols"] = [
                    s.strip() for s in subprotocols_str.split(",") if s.strip()
                ]
                if not body_stream._done:
                    await body_stream.discard()
                await _handle_asgi_websocket(reader, writer, app, scope, raw_headers)
                break
            else:
                await _handle_asgi_http(writer, app, scope, body_stream)

            connection = raw_headers.get("connection", "")
            if "close" in connection.lower():
                break
    except (
        asyncio.IncompleteReadError,
        ConnectionError,
        ConnectionResetError,
        OSError,
    ):
        pass
    finally:
        try:
            writer.close()
            await writer.wait_closed()
        except Exception:
            pass


async def _handle_asgi_lifespan(app, state):
    """Run ASGI lifespan protocol. Returns (success, shutdown_coroutine)."""
    scope = {
        "type": "lifespan",
        "asgi": _ASGI_VERSION,
        "state": state,
    }

    startup_complete = asyncio.Event()
    shutdown_complete = asyncio.Event()
    startup_failed = False

    receive_queue = asyncio.Queue()

    async def receive():
        return await receive_queue.get()

    async def send(message):
        nonlocal startup_failed
        msg_type = message["type"]
        if msg_type == "lifespan.startup.complete":
            startup_complete.set()
        elif msg_type == "lifespan.startup.failed":
            startup_failed = True
            msg = message.get("message", "")
            if msg:
                print(f"Lifespan startup failed: {msg}", file=sys.stderr)
            startup_complete.set()
        elif msg_type == "lifespan.shutdown.complete":
            shutdown_complete.set()
        elif msg_type == "lifespan.shutdown.failed":
            msg = message.get("message", "")
            if msg:
                print(f"Lifespan shutdown failed: {msg}", file=sys.stderr)
            shutdown_complete.set()

    async def run():
        try:
            await app(scope, receive, send)
        except Exception:
            traceback.print_exc(file=sys.stderr)
            if not startup_complete.is_set():
                startup_complete.set()

    lifespan_task = asyncio.create_task(run())

    await receive_queue.put({"type": "lifespan.startup"})
    await startup_complete.wait()

    if startup_failed:
        lifespan_task.cancel()
        return False, None

    async def do_shutdown():
        await receive_queue.put({"type": "lifespan.shutdown"})
        try:
            await asyncio.wait_for(shutdown_complete.wait(), timeout=30)
        except TimeoutError:
            print("Lifespan shutdown timed out", file=sys.stderr)
        lifespan_task.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await lifespan_task

    return True, do_shutdown


async def run_asgi_server(app, socket_path, lifespan):
    """Run an ASGI server. On Unix: Unix socket at socket_path. On Windows: TCP, write port to socket_path."""
    state = {}
    shutdown_fn = None

    if lifespan:
        ok, shutdown_fn = await _handle_asgi_lifespan(app, state)
        if not ok:
            print("ASGI lifespan startup failed, exiting", file=sys.stderr)
            sys.exit(1)

    if _USE_TCP:
        server = await asyncio.start_server(
            lambda r, w: _handle_asgi_connection(r, w, app, state),
            "127.0.0.1",
            0,
        )
        port = server.sockets[0].getsockname()[1]
        try:
            _write_port_file_atomic(socket_path, port)
        except OSError as e:
            print(f"Failed to write port file: {e}", file=sys.stderr)
            sys.exit(1)
        print(f"ASGI server listening on 127.0.0.1:{port}", file=sys.stderr)
    else:
        if os.path.exists(socket_path):
            os.unlink(socket_path)
        server = await asyncio.start_unix_server(
            lambda r, w: _handle_asgi_connection(r, w, app, state),
            path=socket_path,
        )
        print(f"ASGI server listening on {socket_path}", file=sys.stderr)

    stop_event = asyncio.Event()
    loop = asyncio.get_running_loop()
    if sys.platform == "win32":
        # add_signal_handler raises NotImplementedError on Windows; use signal.signal
        def _stop(*args):
            stop_event.set()

        signal.signal(signal.SIGTERM, _stop)
        signal.signal(signal.SIGINT, _stop)
    else:
        try:
            for sig in (signal.SIGTERM, signal.SIGINT):
                loop.add_signal_handler(sig, stop_event.set)
        except (ValueError, OSError):

            def _stop(*args):
                stop_event.set()

            signal.signal(signal.SIGTERM, _stop)
            signal.signal(signal.SIGINT, _stop)

    await stop_event.wait()

    server.close()
    await server.wait_closed()

    if shutdown_fn:
        await shutdown_fn()

    sys.stderr.flush()
    sys.stdout.flush()

    try:
        if os.path.exists(socket_path):
            os.unlink(socket_path)
    except OSError:
        pass


# ==================== Main Entry Point ====================


def main():
    parser = argparse.ArgumentParser(description="caddy-snake Python server")
    parser.add_argument(
        "--socket",
        required=True,
        help="Platform path: Unix socket path (Unix) or port file path (Windows)",
    )
    parser.add_argument("--app", required=True, help="Application module:variable")
    parser.add_argument(
        "--interface",
        required=True,
        choices=["wsgi", "asgi", "esgi"],
        help="Server interface type",
    )
    parser.add_argument("--working-dir", default="", help="Working directory")
    parser.add_argument("--venv", default="", help="Virtual environment path")
    parser.add_argument(
        "--lifespan", default="off", choices=["on", "off"], help="ASGI lifespan events"
    )
    parser.add_argument(
        "--runtime",
        default="",
        help="Worker runtime: wsgi uses sync|gevent; esgi uses gevent only; asgi uses native|uvloop",
    )

    args = parser.parse_args()

    setup_paths(args.working_dir, args.venv)
    app = import_app(args.app)

    if args.interface == "wsgi":
        run_wsgi_server(app, args.socket, args.runtime or "sync")
    elif args.interface == "asgi":
        _configure_asgi_event_loop(args.runtime or "uvloop")
        asyncio.run(run_asgi_server(app, args.socket, args.lifespan == "on"))
    else:
        run_esgi_server(app, args.socket, args.runtime or "gevent")


if __name__ == "__main__":
    main()
