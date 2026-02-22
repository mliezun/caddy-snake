#!/usr/bin/env python3
"""
caddysnake - Self-contained WSGI/ASGI server for caddy-snake.

Listens on a Unix domain socket and serves a Python WSGI or ASGI application.
Designed to be spawned as a subprocess by the caddy-snake Go module.

Usage:
    python caddysnake.py --socket /path/to/sock --app module:variable \
        --interface wsgi|asgi [--working-dir DIR] [--venv DIR] [--lifespan on|off]
"""

import argparse
import asyncio
import base64
import concurrent.futures
import hashlib
import importlib
import io
import os
import signal
import struct
import sys
import traceback
from http import HTTPStatus
from urllib.parse import unquote


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
                return host, int(rest[1:])
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


_wsgi_executor = concurrent.futures.ThreadPoolExecutor(
    max_workers=min(128, (os.cpu_count() or 1) * 8 + 16)
)

_ASGI_VERSION = {"version": "3.0", "spec_version": "2.3"}

_DRAIN_HIGH_WATER = 64 * 1024


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


def _build_wsgi_environ(method, path, version, headers_list, raw_headers, body):
    """Build a WSGI environ dict from parsed HTTP request components."""
    path_part, query_string = _split_path(path)
    host_str = raw_headers.get("host", "localhost")
    server_name, server_port = _parse_host_header(host_str)

    environ = {
        "REQUEST_METHOD": method,
        "SCRIPT_NAME": "",
        "PATH_INFO": unquote(path_part) if "%" in path_part else path_part,
        "QUERY_STRING": query_string,
        "SERVER_NAME": server_name,
        "SERVER_PORT": str(server_port),
        "SERVER_PROTOCOL": version,
        "REMOTE_ADDR": "127.0.0.1",
        "REMOTE_HOST": "127.0.0.1",
        "X_FROM": "caddy-snake",
        "wsgi.version": (1, 0),
        "wsgi.url_scheme": "http",
        "wsgi.input": io.BytesIO(body),
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
        if key_upper in ("CONTENT_TYPE", "CONTENT_LENGTH", "PROXY"):
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

            method, path, version, headers_list, raw_headers, body = result
            environ = _build_wsgi_environ(
                method, path, version, headers_list, raw_headers, body
            )

            status_code, resp_headers, resp_body = await loop.run_in_executor(
                _wsgi_executor, _call_wsgi_app, app, environ
            )

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
            with open(socket_path, "w") as f:
                f.write(str(port))
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


def run_wsgi_server(app, socket_path):
    """Run a WSGI server. On Unix: Unix socket at socket_path. On Windows: TCP, write port to socket_path."""
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


async def _read_http_request(reader):
    """Parse an HTTP/1.1 request from an asyncio reader.

    Returns None on EOF, or (method, path, version, headers, raw_headers, body).
    headers is a list of (name_bytes, value_bytes) for ASGI scope.
    raw_headers is a dict of lowercase-name -> value for internal use.
    """
    try:
        header_data = await reader.readuntil(b"\r\n\r\n")
    except (asyncio.IncompleteReadError, asyncio.LimitOverrunError):
        return None

    lines = header_data[:-4].split(b"\r\n")
    if not lines:
        return None

    request_line = lines[0].decode("latin-1")
    parts = request_line.split(" ", 2)
    if len(parts) != 3:
        return None

    method, path, version = parts

    headers = []
    raw_headers = {}
    for line in lines[1:]:
        colon = line.find(b":")
        if colon < 0:
            continue
        name = line[:colon].strip().lower()
        value = line[colon + 1 :].strip()
        headers.append((name, value))
        raw_headers[name.decode("latin-1")] = value.decode("latin-1")

    body = b""
    content_length = raw_headers.get("content-length")
    transfer_encoding = raw_headers.get("transfer-encoding", "")

    if content_length:
        body = await reader.readexactly(int(content_length))
    elif "chunked" in transfer_encoding.lower():
        chunks = bytearray()
        while True:
            line = await reader.readline()
            chunk_size = int(line.strip(), 16)
            if chunk_size == 0:
                await reader.readline()
                break
            chunks.extend(await reader.readexactly(chunk_size))
            await reader.readline()
        body = bytes(chunks)

    return method, path, version, headers, raw_headers, body


def _encode_header(name, value):
    """Ensure header name and value are bytes."""
    if isinstance(name, str):
        name = name.encode("latin-1")
    if isinstance(value, str):
        value = value.encode("latin-1")
    return name, value


async def _handle_asgi_http(writer, app, scope, body):
    """Handle an ASGI HTTP connection."""
    body_sent = False
    disconnect_event = asyncio.Event()

    async def receive():
        nonlocal body_sent
        if not body_sent:
            body_sent = True
            return {"type": "http.request", "body": body, "more_body": False}
        await disconnect_event.wait()
        return {"type": "http.disconnect"}

    response_started = False
    response_complete = False
    use_chunked = False
    pending_headers = None
    has_cl = False
    has_te = False

    async def send(message):
        nonlocal response_started, response_complete, use_chunked
        nonlocal pending_headers, has_cl, has_te
        msg_type = message["type"]

        if msg_type == "http.response.start":
            response_started = True
            status = message["status"]
            resp_headers = message.get("headers", [])

            buf = bytearray()
            buf.extend(_get_status_line(status))
            for h_name, h_value in resp_headers:
                h_name, h_value = _encode_header(h_name, h_value)
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
                writer.write(buf)
            else:
                if use_chunked:
                    if body_data:
                        writer.write(
                            f"{len(body_data):x}\r\n".encode("ascii")
                            + body_data
                            + b"\r\n"
                        )
                    if not more_body:
                        writer.write(b"0\r\n\r\n")
                else:
                    if body_data:
                        writer.write(body_data)

            if not more_body:
                await writer.drain()
                response_complete = True
                disconnect_event.set()

    try:
        await app(scope, receive, send)
    except Exception:
        traceback.print_exc(file=sys.stderr)
        if not response_started:
            writer.write(
                b"HTTP/1.1 500 Internal Server Error\r\n"
                b"Content-Length: 21\r\n\r\n"
                b"Internal Server Error"
            )
            await writer.drain()
        else:
            if pending_headers is not None:
                pending_headers.extend(b"\r\n")
                writer.write(pending_headers)
                pending_headers = None
            if use_chunked and not response_complete:
                writer.write(b"0\r\n\r\n")
            await writer.drain()


async def _ws_read_loop(reader, receive_queue, closed_event, writer):
    """Read WebSocket frames and enqueue ASGI receive events."""
    try:
        while not closed_event.is_set():
            fin, opcode, payload = await ws_read_frame(reader)

            if opcode == WS_OPCODE_TEXT:
                await receive_queue.put(
                    {"type": "websocket.receive", "text": payload.decode("utf-8")}
                )
            elif opcode == WS_OPCODE_BINARY:
                await receive_queue.put({"type": "websocket.receive", "bytes": payload})
            elif opcode == WS_OPCODE_CLOSE:
                code = 1005
                if len(payload) >= 2:
                    code = struct.unpack("!H", payload[:2])[0]
                await receive_queue.put({"type": "websocket.disconnect", "code": code})
                break
            elif opcode == WS_OPCODE_PING:
                frame = ws_build_frame(WS_OPCODE_PONG, payload)
                writer.write(frame)
                await writer.drain()
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
            read_task = asyncio.create_task(
                _ws_read_loop(reader, receive_queue, ws_closed, writer)
            )

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
                        b"HTTP/1.1 403 Forbidden\r\n"
                        b"Content-Length: 13\r\n\r\n"
                        b"403 Forbidden"
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
            try:
                await read_task
            except asyncio.CancelledError:
                pass


async def _handle_asgi_connection(reader, writer, app, state):
    """Handle a single TCP connection (supports keep-alive)."""
    try:
        while True:
            result = await _read_http_request(reader)
            if result is None:
                break

            method, path, version, headers, raw_headers, body = result
            path_part, query_string = _split_path(path)

            is_websocket = (
                method == "GET"
                and raw_headers.get("upgrade", "").lower() == "websocket"
                and "upgrade" in raw_headers.get("connection", "").lower()
            )

            host_str = raw_headers.get("host", "localhost")
            server_host, server_port = _parse_host_header(host_str)

            if is_websocket:
                conn_type = "websocket"
                scheme = "ws"
            else:
                conn_type = "http"
                scheme = "http"

            scope = {
                "type": conn_type,
                "asgi": _ASGI_VERSION,
                "http_version": version.split("/")[-1] if "/" in version else "1.1",
                "method": method,
                "path": unquote(path_part) if "%" in path_part else path_part,
                "raw_path": path_part.encode("latin-1"),
                "query_string": query_string.encode("latin-1"),
                "root_path": "",
                "scheme": scheme,
                "headers": headers,
                "server": (server_host, server_port),
                "client": ("127.0.0.1", 0),
            }

            if state is not None:
                scope["state"] = dict(state)

            if is_websocket:
                subprotocols_str = raw_headers.get("sec-websocket-protocol", "")
                scope["subprotocols"] = [
                    s.strip() for s in subprotocols_str.split(",") if s.strip()
                ]
                await _handle_asgi_websocket(reader, writer, app, scope, raw_headers)
                break
            else:
                await _handle_asgi_http(writer, app, scope, body)

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
        except asyncio.TimeoutError:
            print("Lifespan shutdown timed out", file=sys.stderr)
        lifespan_task.cancel()
        try:
            await lifespan_task
        except asyncio.CancelledError:
            pass

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
            with open(socket_path, "w") as f:
                f.write(str(port))
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
    try:
        import uvloop

        asyncio.set_event_loop_policy(uvloop.EventLoopPolicy())
    except ImportError:
        pass

    parser = argparse.ArgumentParser(description="caddy-snake Python server")
    parser.add_argument("--socket", required=True, help="Unix socket path")
    parser.add_argument("--app", required=True, help="Application module:variable")
    parser.add_argument(
        "--interface",
        required=True,
        choices=["wsgi", "asgi"],
        help="Server interface type",
    )
    parser.add_argument("--working-dir", default="", help="Working directory")
    parser.add_argument("--venv", default="", help="Virtual environment path")
    parser.add_argument(
        "--lifespan", default="off", choices=["on", "off"], help="ASGI lifespan events"
    )

    args = parser.parse_args()

    setup_paths(args.working_dir, args.venv)
    app = import_app(args.app)

    if args.interface == "wsgi":
        run_wsgi_server(app, args.socket)
    else:
        asyncio.run(run_asgi_server(app, args.socket, args.lifespan == "on"))


if __name__ == "__main__":
    main()
