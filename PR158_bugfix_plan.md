# PR #158 Bug-Fix Plan (High + Medium Findings)

## Scope
This plan addresses the six review findings for `python-based-implementation` vs `main`:

1. Request-body buffering DoS risk (high).
2. WebSocket fragmentation ignored (high).
3. Unsafe UTF-8 decode per frame (medium).
4. ASGI request body not streamed (medium).
5. Windows port-file race (medium).
6. Autoreload failure exiting entire Caddy process (medium).

## Implementation Order
1. Refactor Python request parsing/body handling to support streaming in one pass.
2. Fix WebSocket message reassembly and UTF-8 decoding behavior.
3. Fix Windows port-file handoff race (Python writer + Go reader).
4. Remove `os.Exit`-driven autoreload failure path and adjust behavior/tests.
5. Run targeted tests, then full required test suite.

## Detailed Plan

### 1) Streaming Request Bodies End-to-End (Fixes #1 and #4)

#### Files
- `caddysnake.py`
- `caddysnake_test.py`

#### Planned changes
- Split current request parsing into:
  - header parsing (`method/path/version/headers`), and
  - incremental body reader (Content-Length and chunked transfer decoding).
- Introduce a body-stream abstraction with bounded chunk reads and cumulative byte accounting against `MAX_BODY_SIZE` while data is consumed (not pre-buffered).
- ASGI path:
  - Change `_handle_asgi_http` to consume from body stream and emit repeated `{"type": "http.request", "body": <chunk>, "more_body": True}` events.
  - Emit final `http.request` with `more_body=False` only after full body consumption.
  - Keep `http.disconnect` behavior for post-body receive calls.
- WSGI path:
  - Replace `io.BytesIO(body)` with a custom file-like bridge for `wsgi.input` that pulls from the async stream on-demand.
  - Support common WSGI input methods (`read`, `readline`, `readlines`, iterator protocol) so Flask/Django/FastAPI-on-WSGI adapters keep working.
  - Ensure body reads are lazy and bounded; do not materialize full request body in memory by default.

#### Key guardrails
- Preserve existing rejection behavior for malformed requests and ambiguous `Content-Length` + `Transfer-Encoding`.
- Preserve current max request/header limits.
- Avoid deadlocks between asyncio loop and WSGI threadpool reads.

#### Tests to add/update
- `TestReadHttpRequest` updates to assert no full-body return contract (new stream contract).
- New ASGI test: large body arrives as multiple `http.request` events (`more_body=True` then final false).
- New ASGI test: chunked upload is streamed in event sequence order.
- New WSGI test: app reads request body incrementally from `wsgi.input` (`read(size)` loop) and receives complete payload.
- New WSGI test: line-based reads (`readline`) over chunked transfer body.

### 2) WebSocket Fragmentation + UTF-8 Safety (Fixes #2 and #3)

#### Files
- `caddysnake.py`
- `caddysnake_test.py`

#### Planned changes
- Rework `_ws_read_loop` to implement message assembly state:
  - Track fragmented message type (`text`/`binary`), fragment buffer, and finalization on `fin=1`.
  - Accept opcode `0x0` continuation frames only when a fragmented message is active.
  - Handle protocol errors (unexpected continuation, new data frame during active fragmented message, invalid control frame fragmentation) as clean disconnect/close with protocol error semantics.
- Decode UTF-8 only after the entire text message is fully reassembled.
- Keep ping/pong and close handling compliant with control-frame constraints.

#### Tests to add/update
- New WebSocket test: fragmented text message (first frame `TEXT fin=0`, then `CONTINUATION fin=1`) is emitted as one `websocket.receive{text=...}` event.
- New WebSocket test: fragmented binary message reassembles correctly.
- New WebSocket test: split multi-byte UTF-8 character across fragments succeeds after final assembly.
- New WebSocket test: invalid continuation sequence triggers disconnect with protocol-error behavior.

### 3) Windows Port File Race (Fix #5)

#### Files
- `caddysnake.py`
- `caddysnake.go`
- `caddysnake_test.go`

#### Planned changes
- Python writer side (`_USE_TCP` branches in WSGI/ASGI server startup):
  - Write port to temp file in same directory.
  - Flush and `fsync`.
  - Atomically replace destination path with `os.replace()`.
- Go reader side (`waitForPortFile`):
  - Continue retrying on transient read/parse failures (including empty/partial content).
  - Improve resilience to Windows sharing-violation style errors (retry instead of failing fast).
  - Keep timeout-based failure semantics.

#### Tests to add/update
- Add/adjust `waitForPortFile` tests to cover transient invalid content before valid content appears.
- Add test for retry-until-valid behavior under repeated read failures (best-effort cross-platform simulation).

### 4) Autoreload Should Not Terminate Whole Process (Fix #6)

#### Files
- `caddysnake.go`
- `autoreload.go`
- `dynamic.go`
- `autoreload_test.go`
- `caddysnake_test.go`

#### Planned changes
- Remove `os.Exit` callback wiring from module provisioning paths (`Provision`/`provisionDynamic`).
- Keep autoreload failures scoped to the affected app instance instead of process-wide termination.
- Preserve explicit logging on reload/import failure with enough context (module, working dir, error).
- Ensure failure mode remains recoverable:
  - `AutoreloadableApp`: remain in degraded mode (`errorApp`) or retain previous app depending on final behavior choice (see questions below), but never exit host process.
  - `DynamicApp`: return request error / recover on next successful import without terminating Caddy.

#### Tests to add/update
- Replace “terminates when exit func set” expectations in `autoreload_test.go` and `caddysnake_test.go` with non-termination assertions.
- Add assertions that failures are logged and subsequent successful reload/import restores normal serving behavior.

## Verification Plan

### Targeted during implementation
- `go test -v ./...` (or package-focused runs while iterating)
- `pytest caddysnake_test.py -v`

### Required before merge (per AGENTS.md)
- `go test -race -v .`
- `pytest caddysnake_test.py -v`
- `./tests/integration.sh flask 3.13`
- `./tests/integration.sh fastapi 3.13`

## Risks and Mitigations
1. WSGI stream bridge deadlocks between asyncio loop and threadpool reads; mitigate by explicit producer/consumer contract, bounded buffers, and deterministic EOF signaling tests.
2. ASGI receive contract regressions for small bodies; mitigate with backward-compatible tests for empty/single-chunk requests.
3. WebSocket protocol regressions with control frames; mitigate by explicit ping/close + fragmented-data tests.
4. Windows race fix incomplete due platform differences in CI; mitigate with deterministic retry logic and atomic writer update on Python side.

## Open Questions
1. On autoreload failure in `AutoreloadableApp`, do we prefer to keep serving the last known-good app (higher availability) or switch to `errorApp` 503 until next successful reload (more explicit failure)?
2. For WSGI body streaming, should we enforce a smaller per-request in-memory buffer threshold before spooling to disk, or keep pure streaming with bounded in-memory chunks only?
3. Do you want me to include `dynamic.go` autoreload behavior in the same patch as fix #6, even though the review item called out `caddysnake.go` directly?
