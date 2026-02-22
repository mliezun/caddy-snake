"""Tests for caddysnake.py - WSGI/ASGI server for caddy-snake."""

import asyncio
import os
import struct
import sys
import tempfile
from unittest import mock

import pytest

# Import caddysnake module (caddysnake.py in project root)
import caddysnake as cs


# ==================== Pure Functions ====================


class TestStatusPhrase:
    def test_valid_codes(self):
        assert cs._status_phrase(200) == "OK"
        assert cs._status_phrase(404) == "Not Found"
        assert cs._status_phrase(500) == "Internal Server Error"
        assert cs._status_phrase(201) == "Created"

    def test_unknown_code(self):
        assert cs._status_phrase(999) == "Unknown"


class TestParseHostHeader:
    def test_empty(self):
        assert cs._parse_host_header("") == ("localhost", 80)
        assert cs._parse_host_header(None) == ("localhost", 80)

    def test_host_only(self):
        assert cs._parse_host_header("example.com") == ("example.com", 80)
        assert cs._parse_host_header("localhost") == ("localhost", 80)

    def test_host_port(self):
        assert cs._parse_host_header("example.com:8080") == ("example.com", 8080)
        assert cs._parse_host_header("localhost:443") == ("localhost", 443)

    def test_ipv6_no_port(self):
        assert cs._parse_host_header("[::1]") == ("::1", 80)
        assert cs._parse_host_header("[2001:db8::1]") == ("2001:db8::1", 80)

    def test_ipv6_with_port(self):
        assert cs._parse_host_header("[::1]:8080") == ("::1", 8080)
        assert cs._parse_host_header("[::1]:443") == ("::1", 443)

    def test_custom_default_port(self):
        assert cs._parse_host_header("example.com") == ("example.com", 80)
        assert cs._parse_host_header("example.com", default_port=443) == (
            "example.com",
            443,
        )

    def test_invalid_port_fallback(self):
        # Invalid port (e.g. "abc") falls back to default, host stays as-is
        assert cs._parse_host_header("host:abc") == ("host:abc", 80)
        assert cs._parse_host_header("host:abc", default_port=443) == (
            "host:abc",
            443,
        )


class TestWsBuildFrame:
    def test_small_payload(self):
        frame = cs.ws_build_frame(cs.WS_OPCODE_TEXT, b"hello")
        assert len(frame) == 2 + 5
        assert frame[0] == 0x81  # fin=1, opcode=1
        assert frame[1] == 5

    def test_medium_payload_126(self):
        payload = b"x" * 200
        frame = cs.ws_build_frame(cs.WS_OPCODE_BINARY, payload)
        assert frame[0] == 0x82
        assert frame[1] == 126
        assert struct.unpack("!H", frame[2:4])[0] == 200
        assert frame[4:] == payload

    def test_large_payload_127(self):
        payload = b"x" * 65536
        frame = cs.ws_build_frame(cs.WS_OPCODE_BINARY, payload)
        assert frame[0] == 0x82
        assert frame[1] == 127
        assert struct.unpack("!Q", frame[2:10])[0] == 65536
        assert frame[10:] == payload

    def test_empty_payload(self):
        frame = cs.ws_build_frame(cs.WS_OPCODE_PING, b"")
        assert frame[0] == 0x89
        assert frame[1] == 0
        assert len(frame) == 2

    def test_fin_false(self):
        frame = cs.ws_build_frame(cs.WS_OPCODE_TEXT, b"x", fin=False)
        assert frame[0] == 0x01  # fin=0


class TestWsAcceptKey:
    def test_rfc6455_example(self):
        # RFC 6455 section 1.3 example
        key = "dGhlIHNhbXBsZSBub25jZQ=="
        accept = cs.ws_accept_key(key)
        assert accept == "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="

    def test_deterministic(self):
        key = "test-key-123"
        assert cs.ws_accept_key(key) == cs.ws_accept_key(key)


class TestEncodeHeader:
    def test_str_name_value(self):
        n, v = cs._encode_header("Content-Type", "text/plain")
        assert n == b"Content-Type"
        assert v == b"text/plain"

    def test_bytes_name_value(self):
        n, v = cs._encode_header(b"X-Custom", b"value")
        assert n == b"X-Custom"
        assert v == b"value"

    def test_mixed(self):
        n, v = cs._encode_header("Name", b"value")
        assert n == b"Name"
        assert v == b"value"
        n, v = cs._encode_header(b"Name", "value")
        assert n == b"Name"
        assert v == b"value"


# ==================== import_app ====================


class TestImportApp:
    def test_valid_wsgi(self):
        app = cs.import_app("tests.test_apps.minimal:app")
        assert callable(app)

    def test_valid_asgi(self):
        app = cs.import_app("tests.test_apps.minimal_asgi:app")
        assert callable(app)

    def test_no_colon(self):
        with pytest.raises(ValueError, match="Expected 'module:variable' format"):
            cs.import_app("nomodule")

    def test_too_many_colons(self):
        with pytest.raises(ValueError, match="Expected 'module:variable' format"):
            cs.import_app("a:b:c")

    def test_empty_string(self):
        with pytest.raises(ValueError, match="Expected 'module:variable' format"):
            cs.import_app("")

    def test_nonexistent_module(self):
        with pytest.raises(ModuleNotFoundError):
            cs.import_app("nonexistent_module_xyz:app")

    def test_nonexistent_attribute(self):
        with pytest.raises(AttributeError):
            cs.import_app("tests.test_apps.minimal:nonexistent")


# ==================== setup_paths ====================


class TestSetupPaths:
    def test_working_dir_adds_to_path_and_chdir(self):
        with tempfile.TemporaryDirectory() as tmp:
            orig_cwd = os.getcwd()
            orig_path = sys.path.copy()
            try:
                cs.setup_paths(tmp, None)
                abs_tmp = os.path.abspath(tmp)
                assert abs_tmp in sys.path
                assert os.path.realpath(os.getcwd()) == os.path.realpath(abs_tmp)
            finally:
                os.chdir(orig_cwd)
                sys.path[:] = orig_path

    def test_working_dir_idempotent_path(self):
        with tempfile.TemporaryDirectory() as tmp:
            orig_cwd = os.getcwd()
            orig_path = sys.path.copy()
            try:
                abs_tmp = os.path.abspath(tmp)
                sys.path.insert(0, abs_tmp)
                cs.setup_paths(tmp, None)
                # Should not duplicate
                count = sys.path.count(abs_tmp)
                assert count == 1
            finally:
                os.chdir(orig_cwd)
                sys.path[:] = orig_path

    def test_venv_unix_site_packages(self):
        if sys.platform == "win32":
            pytest.skip("Unix venv layout")
        with tempfile.TemporaryDirectory() as venv_root:
            py_ver = f"python3.{sys.version_info.minor}"
            site_pkgs = os.path.join(venv_root, "lib", py_ver, "site-packages")
            os.makedirs(site_pkgs, exist_ok=True)
            orig_path = sys.path.copy()
            try:
                cs.setup_paths(None, venv_root)
                assert site_pkgs in sys.path
            finally:
                sys.path[:] = orig_path

    def test_venv_missing_python_dir(self, capsys):
        if sys.platform == "win32":
            pytest.skip("Unix venv layout")
        with tempfile.TemporaryDirectory() as venv_root:
            lib_dir = os.path.join(venv_root, "lib")
            os.makedirs(lib_dir, exist_ok=True)
            # No python3.* subdir
            orig_path = sys.path.copy()
            cs.setup_paths(None, venv_root)
            sys.path[:] = orig_path
            captured = capsys.readouterr()
            assert "could not find python3" in captured.err.lower()

    def test_venv_missing_site_packages(self, capsys):
        if sys.platform == "win32":
            pytest.skip("Unix venv layout")
        with tempfile.TemporaryDirectory() as venv_root:
            py_ver = f"python3.{sys.version_info.minor}"
            os.makedirs(os.path.join(venv_root, "lib", py_ver), exist_ok=True)
            # No site-packages
            orig_path = sys.path.copy()
            cs.setup_paths(None, venv_root)
            sys.path[:] = orig_path
            captured = capsys.readouterr()
            assert "site-packages not found" in captured.err.lower()

    def test_empty_args_no_effect(self):
        orig_cwd = os.getcwd()
        orig_path = sys.path.copy()
        try:
            cs.setup_paths("", "")
            assert os.path.realpath(os.getcwd()) == os.path.realpath(orig_cwd)
        finally:
            os.chdir(orig_cwd)
            sys.path[:] = orig_path


# ==================== WebSocket ws_read_frame (async) ====================


@pytest.mark.asyncio
class TestWsReadFrame:
    async def test_small_unmasked_frame(self):
        frame = cs.ws_build_frame(cs.WS_OPCODE_TEXT, b"hello")
        reader = asyncio.StreamReader()
        reader.feed_data(frame)
        reader.feed_eof()
        fin, opcode, payload = await cs.ws_read_frame(reader)
        assert fin == 1
        assert opcode == cs.WS_OPCODE_TEXT
        assert payload == b"hello"

    async def test_masked_frame(self):
        # Client frames are masked; build manually
        payload = b"test"
        mask_key = os.urandom(4)
        masked = bytes(b ^ mask_key[i % 4] for i, b in enumerate(payload))
        frame = bytearray()
        frame.append(0x81)  # fin, text
        frame.append(0x80 | len(payload))  # masked
        frame.extend(mask_key)
        frame.extend(masked)
        reader = asyncio.StreamReader()
        reader.feed_data(bytes(frame))
        reader.feed_eof()
        fin, opcode, payload_out = await cs.ws_read_frame(reader)
        assert payload_out == b"test"

    async def test_extended_payload_126(self):
        payload = b"x" * 200
        frame = cs.ws_build_frame(cs.WS_OPCODE_BINARY, payload)
        reader = asyncio.StreamReader()
        reader.feed_data(frame)
        reader.feed_eof()
        fin, opcode, payload_out = await cs.ws_read_frame(reader)
        assert payload_out == payload

    async def test_extended_payload_127(self):
        payload = b"x" * 65536
        frame = cs.ws_build_frame(cs.WS_OPCODE_BINARY, payload)
        reader = asyncio.StreamReader()
        reader.feed_data(frame)
        reader.feed_eof()
        fin, opcode, payload_out = await cs.ws_read_frame(reader)
        assert payload_out == payload

    async def test_empty_payload(self):
        frame = cs.ws_build_frame(cs.WS_OPCODE_PING, b"")
        reader = asyncio.StreamReader()
        reader.feed_data(frame)
        reader.feed_eof()
        fin, opcode, payload = await cs.ws_read_frame(reader)
        assert payload == b""


# ==================== _read_http_request (async) ====================


@pytest.mark.asyncio
class TestReadHttpRequest:
    async def _feed_and_read(self, data: bytes):
        reader = asyncio.StreamReader()
        reader.feed_data(data)
        reader.feed_eof()
        return await cs._read_http_request(reader)

    async def test_simple_get(self):
        req = b"GET /path HTTP/1.1\r\nHost: example.com\r\n\r\n"
        result = await self._feed_and_read(req)
        assert result is not None
        method, path, version, headers, raw_headers, body = result
        assert method == "GET"
        assert path == "/path"
        assert "1.1" in version
        assert raw_headers.get("host") == "example.com"
        assert body == b""

    async def test_post_with_content_length(self):
        req = (
            b"POST / HTTP/1.1\r\n"
            b"Host: localhost\r\n"
            b"Content-Length: 11\r\n"
            b"\r\n"
            b"hello world"
        )
        result = await self._feed_and_read(req)
        assert result is not None
        _, _, _, _, _, body = result
        assert body == b"hello world"

    async def test_chunked_body(self):
        req = (
            b"POST / HTTP/1.1\r\n"
            b"Host: localhost\r\n"
            b"Transfer-Encoding: chunked\r\n"
            b"\r\n"
            b"5\r\nhello\r\n"
            b"6\r\n world\r\n"
            b"0\r\n\r\n"
        )
        result = await self._feed_and_read(req)
        assert result is not None
        _, _, _, _, _, body = result
        assert body == b"hello world"

    async def test_eof_returns_none(self):
        reader = asyncio.StreamReader()
        reader.feed_eof()
        result = await cs._read_http_request(reader)
        assert result is None

    async def test_empty_line_returns_none(self):
        result = await self._feed_and_read(b"\r\n")
        assert result is None

    async def test_malformed_request_line(self):
        result = await self._feed_and_read(b"GET /path\r\n\r\n")
        assert result is None

    async def test_header_without_colon_skipped(self):
        req = b"GET / HTTP/1.1\r\nHost: localhost\r\nBadHeader\r\nX-Good: yes\r\n\r\n"
        result = await self._feed_and_read(req)
        assert result is not None
        _, _, _, headers, raw_headers, _ = result
        assert (b"x-good", b"yes") in headers
        assert "badheader" not in raw_headers


# ==================== ASGI _handle_asgi_http ====================


@pytest.mark.asyncio
class TestHandleAsgiHttp:
    async def test_minimal_app(self):
        async def app(scope, receive, send):
            await send(
                {
                    "type": "http.response.start",
                    "status": 200,
                    "headers": [(b"Content-Type", b"text/plain")],
                }
            )
            await send({"type": "http.response.body", "body": b"ok"})

        reader = asyncio.StreamReader()
        written = []
        writer = mock.Mock()
        writer.write = lambda d: written.append(d)

        async def mock_drain():
            pass

        writer.drain = mock_drain

        req = b"GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"
        reader.feed_data(req)
        reader.feed_eof()

        # Read the request first (normally _handle_asgi_connection does this)
        result = await cs._read_http_request(reader)
        assert result is not None
        method, path, version, headers, raw_headers, body = result

        scope = {
            "type": "http",
            "method": method,
            "path": path,
            "headers": headers,
        }

        await cs._handle_asgi_http(writer, app, scope, body)

        out = b"".join(written)
        assert b"HTTP/1.1 200 OK" in out
        assert b"ok" in out

    async def test_chunked_response_when_no_content_length(self):
        async def app(scope, receive, send):
            await send(
                {
                    "type": "http.response.start",
                    "status": 200,
                    "headers": [],  # no Content-Length
                }
            )
            await send(
                {"type": "http.response.body", "body": b"chunk1", "more_body": True}
            )
            await send(
                {"type": "http.response.body", "body": b"chunk2", "more_body": True}
            )
            await send({"type": "http.response.body", "body": b"", "more_body": False})

        async def _drain():
            pass

        written = []
        writer = mock.Mock()
        writer.write = lambda d: written.append(d)
        writer.drain = _drain

        scope = {"type": "http"}
        await cs._handle_asgi_http(writer, app, scope, b"")

        out = b"".join(written)
        assert b"transfer-encoding: chunked" in out.lower()
        assert b"6\r\nchunk1\r\n" in out
        assert b"6\r\nchunk2\r\n" in out
        assert b"0\r\n\r\n" in out

    async def test_app_exception_before_start_sends_500(self):
        async def app(scope, receive, send):
            raise RuntimeError("boom")

        async def _drain():
            pass

        written = []
        writer = mock.Mock()
        writer.write = lambda d: written.append(d)
        writer.drain = _drain

        scope = {"type": "http"}
        await cs._handle_asgi_http(writer, app, scope, b"")

        out = b"".join(written)
        assert b"500 Internal Server Error" in out


# ==================== ASGI _handle_asgi_websocket ====================


@pytest.mark.asyncio
class TestHandleAsgiWebsocket:
    async def test_accept_sends_upgrade_response(self):
        async def _drain():
            pass

        written = []
        writer = mock.Mock()
        writer.write = lambda d: written.append(d)
        writer.drain = _drain

        ws_key = "dGhlIHNhbXBsZSBub25jZQ=="
        raw_headers = {"sec-websocket-key": ws_key}

        async def app(scope, receive, send):
            msg = await receive()
            assert msg["type"] == "websocket.connect"
            await send({"type": "websocket.accept"})
            await send({"type": "websocket.close"})

        reader = asyncio.StreamReader()
        reader.feed_eof()

        scope = {"type": "websocket"}

        await cs._handle_asgi_websocket(reader, writer, app, scope, raw_headers)

        out = b"".join(written)
        assert b"101 Switching Protocols" in out
        assert b"Upgrade: websocket" in out
        assert b"Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" in out

    async def test_close_before_accept_403(self):
        async def _drain():
            pass

        written = []
        writer = mock.Mock()
        writer.write = lambda d: written.append(d)
        writer.drain = _drain

        raw_headers = {"sec-websocket-key": "test"}

        async def app(scope, receive, send):
            await receive()
            await send({"type": "websocket.close"})

        reader = asyncio.StreamReader()
        reader.feed_eof()

        scope = {"type": "websocket"}
        await cs._handle_asgi_websocket(reader, writer, app, scope, raw_headers)

        out = b"".join(written)
        assert b"403 Forbidden" in out


# ==================== ASGI _handle_asgi_connection ====================


@pytest.mark.asyncio
class TestHandleAsgiConnection:
    async def test_http_request_routed(self):
        req = b"GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
        reader = asyncio.StreamReader()
        reader.feed_data(req)
        reader.feed_eof()

        async def _drain():
            pass

        async def _wait_closed():
            pass

        written = []
        writer = mock.Mock()
        writer.write = lambda d: written.append(d)
        writer.drain = _drain
        writer.close = lambda: None
        writer.wait_closed = _wait_closed

        async def app(scope, receive, send):
            assert scope["type"] == "http"
            await send(
                {
                    "type": "http.response.start",
                    "status": 200,
                    "headers": [],
                }
            )
            await send({"type": "http.response.body", "body": b"hi"})

        await cs._handle_asgi_connection(reader, writer, app, {})

        out = b"".join(written)
        assert b"HTTP/1.1 200" in out
        assert b"hi" in out


# ==================== ASGI _handle_asgi_lifespan ====================


@pytest.mark.asyncio
class TestHandleAsgiLifespan:
    async def test_startup_success(self):
        async def app(scope, receive, send):
            msg = await receive()
            assert msg["type"] == "lifespan.startup"
            await send({"type": "lifespan.startup.complete"})
            msg = await receive()
            assert msg["type"] == "lifespan.shutdown"
            await send({"type": "lifespan.shutdown.complete"})

        state = {}
        ok, shutdown_fn = await cs._handle_asgi_lifespan(app, state)
        assert ok is True
        assert shutdown_fn is not None
        await shutdown_fn()

    async def test_startup_failed(self):
        async def app(scope, receive, send):
            _ = await receive()
            await send({"type": "lifespan.startup.failed", "message": "nope"})

        ok, shutdown_fn = await cs._handle_asgi_lifespan(app, {})
        assert ok is False
        assert shutdown_fn is None


# ==================== WSGI _call_wsgi_app ====================


class TestCallWsgiApp:
    def test_basic_response(self):
        def app(environ, start_response):
            start_response("200 OK", [("Content-Type", "text/plain")])
            return [b"ok"]

        environ = {"REQUEST_METHOD": "GET", "wsgi.input": __import__("io").BytesIO(b"")}
        status, headers, body = cs._call_wsgi_app(app, environ)
        assert status == 200
        assert body == b"ok"
        assert ("Content-Type", "text/plain") in headers

    def test_app_not_calling_start_response(self):
        def app(environ, start_response):
            return [b"bad"]

        environ = {"REQUEST_METHOD": "GET", "wsgi.input": __import__("io").BytesIO(b"")}
        status, headers, body = cs._call_wsgi_app(app, environ)
        assert status == 500

    def test_app_exception(self):
        def app(environ, start_response):
            raise RuntimeError("boom")

        environ = {"REQUEST_METHOD": "GET", "wsgi.input": __import__("io").BytesIO(b"")}
        status, headers, body = cs._call_wsgi_app(app, environ)
        assert status == 500

    def test_multiple_chunks(self):
        def app(environ, start_response):
            start_response("200 OK", [])
            return [b"hello", b" ", b"world"]

        environ = {"REQUEST_METHOD": "GET", "wsgi.input": __import__("io").BytesIO(b"")}
        status, headers, body = cs._call_wsgi_app(app, environ)
        assert status == 200
        assert body == b"hello world"

    def test_close_called(self):
        closed = []

        class Result:
            def __iter__(self):
                return iter([b"data"])

            def close(self):
                closed.append(True)

        def app(environ, start_response):
            start_response("200 OK", [])
            return Result()

        environ = {"REQUEST_METHOD": "GET", "wsgi.input": __import__("io").BytesIO(b"")}
        cs._call_wsgi_app(app, environ)
        assert closed == [True]


# ==================== WSGI _build_wsgi_environ ====================


class TestBuildWsgiEnviron:
    def test_basic_environ(self):
        headers_list = [(b"host", b"example.com:8080")]
        raw_headers = {"host": "example.com:8080"}
        env = cs._build_wsgi_environ(
            "GET", "/foo?bar=baz", "HTTP/1.1", headers_list, raw_headers, b""
        )
        assert env["REQUEST_METHOD"] == "GET"
        assert env["PATH_INFO"] == "/foo"
        assert env["QUERY_STRING"] == "bar=baz"
        assert env["SERVER_NAME"] == "example.com"
        assert env["SERVER_PORT"] == "8080"
        assert env["HTTP_HOST"] == "example.com:8080"
        assert env["X_FROM"] == "caddy-snake"

    def test_path_unquoting(self):
        env = cs._build_wsgi_environ("GET", "/hello%20world", "HTTP/1.1", [], {}, b"")
        assert env["PATH_INFO"] == "hello world" or env["PATH_INFO"] == "/hello world"

    def test_content_type_and_length(self):
        headers_list = [
            (b"content-type", b"application/json"),
            (b"content-length", b"42"),
        ]
        raw_headers = {"content-type": "application/json", "content-length": "42"}
        env = cs._build_wsgi_environ(
            "POST", "/", "HTTP/1.1", headers_list, raw_headers, b"x" * 42
        )
        assert env["CONTENT_TYPE"] == "application/json"
        assert env["CONTENT_LENGTH"] == "42"
        assert "HTTP_CONTENT_TYPE" not in env
        assert "HTTP_CONTENT_LENGTH" not in env

    def test_duplicate_headers_joined(self):
        headers_list = [
            (b"host", b"x"),
            (b"x-custom", b"first"),
            (b"x-custom", b"second"),
        ]
        raw_headers = {"host": "x", "x-custom": "second"}
        env = cs._build_wsgi_environ(
            "GET", "/", "HTTP/1.1", headers_list, raw_headers, b""
        )
        assert "first" in env["HTTP_X_CUSTOM"]
        assert "second" in env["HTTP_X_CUSTOM"]

    def test_cookie_joined_with_semicolon(self):
        headers_list = [
            (b"host", b"x"),
            (b"cookie", b"a=1"),
            (b"cookie", b"b=2"),
        ]
        raw_headers = {"host": "x", "cookie": "b=2"}
        env = cs._build_wsgi_environ(
            "GET", "/", "HTTP/1.1", headers_list, raw_headers, b""
        )
        assert env["HTTP_COOKIE"] == "a=1; b=2"

    def test_proxy_header_excluded(self):
        headers_list = [(b"host", b"x"), (b"proxy", b"evil")]
        raw_headers = {"host": "x", "proxy": "evil"}
        env = cs._build_wsgi_environ(
            "GET", "/", "HTTP/1.1", headers_list, raw_headers, b""
        )
        assert "HTTP_PROXY" not in env


# ==================== WSGI Handler (async) ====================


async def _handle_wsgi_request(app, request_bytes):
    """Feed a raw HTTP request to the async WSGI handler and return the response."""
    reader = asyncio.StreamReader()
    reader.feed_data(request_bytes)
    reader.feed_eof()

    written = []
    writer = mock.Mock()
    writer.write = lambda d: written.append(d)

    async def _drain():
        pass

    writer.drain = _drain
    writer.close = lambda: None

    async def _wait_closed():
        pass

    writer.wait_closed = _wait_closed

    await cs._handle_wsgi_connection(reader, writer, app)
    return b"".join(written)


@pytest.mark.asyncio
class TestWsgiHandler:
    async def test_environ_building(self):
        """WSGI environ has correct keys from request."""
        app_called = []

        def app(environ, start_response):
            app_called.append(dict(environ))
            start_response("200 OK", [])
            return [b"ok"]

        resp = await _handle_wsgi_request(
            app,
            b"GET /foo?bar=baz HTTP/1.1\r\nHost: example.com:8080\r\nConnection: close\r\n\r\n",
        )
        assert b"200 OK" in resp

        env = app_called[0]
        assert env["REQUEST_METHOD"] == "GET"
        assert env["PATH_INFO"] == "/foo"
        assert env["QUERY_STRING"] == "bar=baz"
        assert env["SERVER_NAME"] == "example.com"
        assert env["SERVER_PORT"] == "8080"
        assert env["HTTP_HOST"] == "example.com:8080"
        assert env["X_FROM"] == "caddy-snake"

    async def test_path_unquoting(self):
        def app(environ, start_response):
            start_response("200 OK", [])
            return [environ["PATH_INFO"].encode()]

        resp = await _handle_wsgi_request(
            app, b"GET /hello%20world HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"
        )
        assert b"hello world" in resp

    async def test_content_length_body(self):
        def app(environ, start_response):
            body = environ["wsgi.input"].read()
            start_response("200 OK", [])
            return [body]

        resp = await _handle_wsgi_request(
            app,
            b"POST / HTTP/1.1\r\nHost: x\r\nContent-Length: 5\r\nConnection: close\r\n\r\nhello",
        )
        assert b"hello" in resp

    async def test_chunked_body(self):
        def app(environ, start_response):
            body = environ["wsgi.input"].read()
            start_response("200 OK", [])
            return [body]

        resp = await _handle_wsgi_request(
            app,
            b"POST / HTTP/1.1\r\n"
            b"Host: x\r\n"
            b"Transfer-Encoding: chunked\r\n"
            b"Connection: close\r\n\r\n"
            b"5\r\nhello\r\n0\r\n\r\n",
        )
        assert b"hello" in resp

    async def test_app_must_call_start_response(self):
        def app(environ, start_response):
            return [b"bad"]

        resp = await _handle_wsgi_request(
            app, b"GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"
        )
        assert b"500" in resp

    async def test_duplicate_headers_joined(self):
        def app(environ, start_response):
            start_response("200 OK", [])
            return [environ.get("HTTP_X_CUSTOM", "none").encode()]

        resp = await _handle_wsgi_request(
            app,
            b"GET / HTTP/1.1\r\n"
            b"Host: x\r\n"
            b"X-Custom: first\r\n"
            b"X-Custom: second\r\n"
            b"Connection: close\r\n\r\n",
        )
        assert b"first" in resp

    async def test_cookie_joined_with_semicolon(self):
        def app(environ, start_response):
            start_response("200 OK", [])
            return [environ.get("HTTP_COOKIE", "").encode()]

        resp = await _handle_wsgi_request(
            app,
            b"GET / HTTP/1.1\r\nHost: x\r\nCookie: a=1\r\nCookie: b=2\r\nConnection: close\r\n\r\n",
        )
        assert b"a=1; b=2" in resp

    async def test_response_has_content_length(self):
        """Responses always include Content-Length for keep-alive support."""

        def app(environ, start_response):
            start_response("200 OK", [])
            return [b"hello"]

        resp = await _handle_wsgi_request(
            app, b"GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"
        )
        assert b"Content-Length: 5" in resp


# ==================== main() CLI ====================


class TestMain:
    def test_main_wsgi_calls_run_wsgi_server(self):
        """main() with --interface wsgi calls run_wsgi_server with imported app."""
        with mock.patch.object(cs, "run_wsgi_server") as run_wsgi:
            with mock.patch.object(
                sys,
                "argv",
                [
                    "caddysnake.py",
                    "--socket",
                    "/tmp/test.sock",
                    "--app",
                    "tests.test_apps.minimal:app",
                    "--interface",
                    "wsgi",
                ],
            ):
                # main() would block in run_wsgi_server; mock it to return immediately
                run_wsgi.side_effect = lambda app, sock: None
                cs.main()
                run_wsgi.assert_called_once()
                args = run_wsgi.call_args[0]
                assert callable(args[0])
                assert args[1] == "/tmp/test.sock"

    def test_main_asgi_calls_run_asgi_server(self):
        """main() with --interface asgi calls run_asgi_server with imported app."""

        async def _fake_run_asgi(*args):
            pass

        with mock.patch.object(
            cs, "run_asgi_server", side_effect=_fake_run_asgi
        ) as run_asgi:
            with mock.patch.object(
                sys,
                "argv",
                [
                    "caddysnake.py",
                    "--socket",
                    "/tmp/test.sock",
                    "--app",
                    "tests.test_apps.minimal_asgi:app",
                    "--interface",
                    "asgi",
                ],
            ):
                cs.main()
                run_asgi.assert_called_once()
                args = run_asgi.call_args[0]
                assert callable(args[0])
                assert args[1] == "/tmp/test.sock"
                assert args[2] is False  # lifespan off

    def test_main_invalid_interface(self):
        with mock.patch.object(
            sys,
            "argv",
            [
                "caddysnake.py",
                "--socket",
                "/tmp/s",
                "--app",
                "x:y",
                "--interface",
                "invalid",
            ],
        ):
            with pytest.raises(SystemExit):
                cs.main()
