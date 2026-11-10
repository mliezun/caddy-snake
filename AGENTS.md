# AGENTS.md — Working on caddy-snake

## Ownership

Standing instruction from the maintainer (Miguel Liezun, 2026-11-10), recorded verbatim:

> This codebase is yours: what it's for and how it's built are your call, and you'll keep building on it.

What this means when you work here:

- **Decide, don't defer.** Product direction, architecture, naming inside the code, scope and sequencing are yours to settle. Make the call, write down the reasoning where the next session will find it, and move on. Ask the maintainer only for things that are theirs alone: money, accounts, credentials, legal questions, and anything published under their name.
- **Build for the long run.** You will be the one extending this code later. Prefer designs you can keep building on over the quickest patch, and leave the repo in a state the next session can pick up without this conversation.
- **Keep the record current.** When direction or design changes, update this file and the docs in the same change. Internal planning notes live in `notes/`, which is git-ignored on purpose: they belong to the private repo, not this public one. Read them if they are present locally; never commit them here.
- **Never use the middle dot character (U+00B7, the raised dot between words).** The maintainer dislikes it (2026-11-10). This applies everywhere: pages, docs, messages, comments, commit messages and chat replies. Separate items with a comma, a vertical bar, a dash or a line break.
- **Ownership does not skip the safeguards.** Run the pre-commit checklist, keep docs and the Caddyfile/CLI mapping in sync, and still confirm before pushing, tagging, releasing or publishing — those reach other people and are hard to undo.

## Current direction

caddy-snake is growing from a Caddy plugin that runs Python apps into an open-core platform where people build and share small internal apps with their own AI agent. The open core ships first, in this repo, under the caddy-snake name; a hosted cloud version follows later from a private add-on repo.

The plugin stays usable on its own: new platform features go in their own Go package so plugin-only users do not pull in their dependencies.

---

## Pre-commit checklist

Run the full local QA suite:

```bash
./scripts/qa.sh
```

Or run the checks individually:

1. **Pre-commit hooks**: `pre-commit run --all-files` (install once with `pre-commit install`)
2. **Go tests**: `go test -race -v .`
3. **Go + caddytest** (in-process Caddy, on-demand TLS; needs `python` on `PATH`): `go test -race -tags=caddytest -timeout 180s .`
4. **Python tests** (from the repo root): `pytest caddysnake_test.py -v`
5. **Static checks**: `golangci-lint run ./...`, `ruff check .`, `ruff format --check .`, `uvx ty==0.0.55 check`
6. **Integration tests** — at minimum Flask and FastAPI:
   - `./tests/integration.sh flask 3.13`
   - `./tests/integration.sh fastapi 3.13`
   - Shared cache changes: `./tests/integration.sh simple_cache 3.13`
   - Isolation changes: `./tests/integration.sh simple_isolation 3.13` (requires the host Docker socket)
   - `request_body` changes: `./tests/integration.sh simple_request_body 3.13`
   - `mount_prefix` changes: `./tests/integration.sh simple_mount_prefix 3.13`
   - `apps/` changes: `go test -race ./apps/...` and `./tests/integration.sh apps_e2e 3.13`
7. **Embed-app** (optional, needs network): `cd cmd/embed-app && ./build.sh app.zip 3.13 && ./test_embed.sh embed-test`

CI enforces Go coverage ≥ 65% and Python coverage ≥ 50%, and also runs security scans (govulncheck, pip-audit, gosec, bandit, Semgrep, Gitleaks, Trivy, zizmor). `./scripts/audit-python-deps.sh` runs pip-audit locally.

---

## CI

Rules for every workflow under `.github/workflows/` (maintainer, 2026-11-10: a new push must cancel the runs of the one before it, only the checks that make sense for the change should run, and CI must be fast):

- **Cancel superseded runs.** Every workflow that runs on pull requests has the same `concurrency` block; copy it into new ones.
- **Skip what a change cannot affect.** [`changes.yml`](.github/workflows/changes.yml) works out which areas a pull request touches; other workflows call it as their `changes` job and gate their jobs on its outputs. Pushes to `main`, releases and schedules run everything. Add new areas there, not as `paths:` filters in single workflows.
- **Required checks must still report.** Branch protection requires some checks by their matrix name (`tests (ubuntu-24.04)`, `python-tests (3.13)`, `Analyze (go)`). A skipped matrix job reports under its bare name and would block the pull request for ever, so those jobs always start and skip their *steps* (see `go-tests.yml`). Other jobs are skipped whole with a job-level `if`.
- **Small matrix on pull requests, full matrix on `main`.** The Linux integration tests take their Python versions and runners from `changes.yml`: two Pythons on one runner for pull requests, all of them on `main`.
- **Go cache**: on (`cache: true`) in test-only workflows, off in workflows that build release files.

## Documentation

When you add or change a **user-facing feature** (Caddyfile directives, worker env vars, Python/Go APIs, cache protocol, CLI behavior, integration-test apps), update the docs in the same PR:

1. [`docs/docs/reference.md`](docs/docs/reference.md) — the authoritative configuration and API reference for the plugin.
2. [`docs/docs/apps/`](docs/docs/apps/) — the apps platform.
3. [`README.md`](README.md) — only if the overview changes (see below).
4. [`AGENTS.md`](AGENTS.md) — only when agent or workflow guidance changes.

How the docs are laid out (maintainer, 2026-11-10: lead with apps, keep the plugin's documentation in its own part):

- **Two sections**, each with its own sidebar in [`docs/sidebars.js`](docs/sidebars.js): **Apps** (`docs/docs/apps/`: overview, connecting an assistant, how apps work, sharing, running) and **Caddy plugin** (every other page). The plugin pages keep the addresses they always had (`/docs/intro`, `/docs/reference`, ...); do not move them.
- **The README is an overview, not a reference**: what it is, the demo, a quick start for each of the two, and links into the docs. Directive and flag details live in the docs only. It keeps the benchmark table.
- **Formatting**: one idea per block with space around it. Commands go in fenced code blocks with a language (`bash`, `text`, `toml`), never in a long sentence; anything that differs per operating system goes in `<Tabs groupId="os">`; a list of names and values is a two-column table. The look (spacing, tables, inline code) is set once in `docs/src/css/custom.css`.
- **The demo GIF** (`docs/static/img/apps-demo.gif`) is recorded from a real server by [`scripts/record_demo.py`](scripts/record_demo.py). Record it again when the dashboard changes visibly.
- **The logo** is `docs/static/img/logo.svg`, the same mark as the dashboard's (`logoSVG` and the favicon in `apps/pages.go`). After changing it, run [`scripts/render_logo.sh`](scripts/render_logo.sh) to regenerate the PNGs and the favicon.

For cache or multi-worker features, also cover env vars, the wire protocol (`CS*` commands), the Python API, limits, the security/trust model, and an integration test under `tests/` when behavior spans workers.

If you touch Docusaurus content, build it: `cd docs && npm ci && npm run build`.

---

## Keeping Caddyfile and CLI config in sync

The `python` Caddyfile block and the `python-server` / `caddysnake` CLIs must expose the **same Python-handler configuration**.

Whenever you add or change a `python { ... }` subdirective (or a field on `CaddySnake`):

1. **Caddyfile** — `UnmarshalCaddyfile`, JSON tags, `Validate`, and docs in `docs/docs/reference.md`.
2. **Go CLI** — `caddy python-server` flags in `caddysnake.go` (`CobraFunc` + `pythonServer` wiring into `CaddySnake`).
3. **PyPI CLI wrapper** — pass the same flags through [`cmd/cli/caddysnake_cli.py`](cmd/cli/caddysnake_cli.py) and update [`cmd/cli/README.md`](cmd/cli/README.md).
4. **Docs that list CLI flags** — `docs/docs/reference.md` (the full list), `docs/docs/intro.md`, `docs/docs/installation.md`.

| Caddyfile | CLI flag |
|-----------|----------|
| `module_wsgi` / `module_asgi` / `module_auto` | `--server-type` + `--app` |
| `runtime` | `--runtime` |
| `lifespan` | `--lifespan` |
| `working_dir` | `--working-dir` |
| `venv` | `--venv` |
| `workers` | `--workers` |
| `max_dynamic_apps` | `--max-dynamic-apps` |
| `start_timeout` | `--start-timeout` (indefinite: `--start-timeout=-1` or `forever`) |
| `autoreload` | `--autoreload` |
| `python_path` | `--python-path` |
| `env_file` | `--env-file` (repeatable) |
| `env_var <name> <value>` | `--env-var NAME=VALUE` (repeatable) |
| `isolation docker { image ... }` | `--isolation docker` + `--isolation-image` (+ optional `--isolation-network`, `--isolation-docker-host`, `--isolation-memory`, `--isolation-cpus`, `--isolation-read-only`) |
| `isolation none` | `--isolation none` |
| `cache local` | `--cache-mode local` |
| `cache cluster { ... }` | `--cache-mode cluster` + `--cache-listen` + `--cache-advertise` + repeatable `--cache-peer` + `--cache-namespace` + `--cache-secret` |
| `request_body { max_size <size> }` | `--request-body-max-size` |
| `mount_prefix <path>` | `--mount-prefix` |
| `memory_limit <size>` | `--memory-limit` |

CLI-only conveniences with no Caddyfile equivalent are fine: `--domain`, `--listen` (default `127.0.0.1:9080`), `--static-path`, `--static-route`, `--debug`, `--access-logs`.

---

## The `apps` package

[`apps/`](apps/) is the apps platform (`caddysnake apps` / `caddy python-apps`), documented for users in [`docs/docs/apps/`](docs/docs/apps/). It is a separate Go package on purpose: builds with only `--with github.com/mliezun/caddy-snake` must not pull in its dependencies (Postgres driver, go-git). Release artefacts (PyPI wheel, binaries, Docker image) add `--with github.com/mliezun/caddy-snake/apps`.

| File | What it holds |
|------|---------------|
| `platform.go`, `publish.go`, `drafts.go` | App lifecycle: create, save, publish, roll back, drafts, settings |
| `postgres.go`, `store.go`, `db/` | The built-in database, records, and all SQL (see below) |
| `repo.go` | Version storage (git, written with go-git) |
| `tools.go` | The tools — one implementation shared by MCP, the HTTP API and `call` |
| `mcp.go`, `http.go`, `githttp.go`, `auth.go` | MCP endpoint, the Caddy handler `python_apps`, git remote, logins |
| `pages.go`, `dashboard.go`, `manage.go` | Page markup and styles, page handlers, and the people and access-key actions |
| `command.go`, `config.go`, `install.go` | The `python-apps` command and the Caddy config it builds, the settings file, and installing as a service (systemd on Linux, launchd on macOS, the service manager on Windows) |
| `os.go`, `admin_*.go` | What differs between operating systems: program names, the Python inside an app's environment, where Postgres is, and the `current` link (a symbolic link, a junction on Windows) |
| `guide.go` | The instructions given to AI assistants, and the starter app |

Rules that are easy to break:

- **App packages** come from `pyproject.toml` (installed with `uv sync`, following a `uv.lock` exactly) or `requirements.txt`; with both, `pyproject.toml` wins. Plain HTML without a build step is what the guide recommends to assistants, not a rule the platform enforces.
- **Plain language.** The audience is people who do not program. Anything they or their assistant may read — tool descriptions, errors, pages, the guide — says app, version, draft, publish, settings; not repository, commit, branch, deploy, environment variable.
- **Page design** (maintainer, 2026-11-10, in his words: "it needs to feel like a rock, super solid software"; "reduce the amount of slop text, avoid too many explanations"; "for inspiration look at corporate pages like oracle or aws"; "modernize the style, current trend is liquid glass"). In `pages.go` that means: the *structure* of an enterprise console (global bar, side navigation, panels with a title, a count and actions, filterable tables, a details grid, tabs, dialogs) in a *liquid glass* material (translucent blurred chrome with a bright top edge, capsule controls, floating over a soft ambient background). Glass is for the chrome; panels that hold text stay near-opaque, and the solid fallbacks for reduced transparency stay. Copy is labels and data: no lead paragraphs, no inline tutorials, one short line at most for an empty state or a dialog. No separate breadcrumb bar under the main bar.
- **No accent bars** (maintainer, 2026-11-10: a coloured vertical bar marking the selected item "gives me vibe-coded sensation"). Never mark a selected item, an alert, a card or a notice with a thick coloured stripe along one edge. Show selection with the weight and colour of the text.
- **Settings and install are cautious by design.** Flags override the settings file; a file that is unreadable or has unknown names is an error, never skipped; `install` never overwrites an existing settings file, supports `--dry-run`, and `uninstall` keeps settings and data unless `--purge`. Every new server flag also needs a `Settings` field (`config.go`) and a row in the docs table.
- **Windows runs it too.** Keep operating-system differences in `os.go` behind functions that take `goos`, so they are tested on any machine. Windows cannot replace a running program, cannot rename over a folder, keeps a pipe open in programs left running (see `runDetached`), and names its accounts differently per language (use the fixed identifiers in `install.go`).
- **go-git 6 is a beta** (`v6.0.0-beta.1`). Version 5 has known problems that are only fixed in 6, so we moved early; only `apps/repo.go` uses it. Move to the stable 6.0 when it is out. Snyk reports five go-git problems that no release fixes; the maintainer accepted them (2026-11-10) and they are recorded in [`.snyk`](.snyk) with the reasons and an expiry date of 2027-02-10. When they expire, look for a fixed release first; renew only if the reasons still hold.
- **Workers run from the real release folder, not from `current`.** The plugin follows the link when it starts a worker (`pinPath`), and throws a start away if the link moved meanwhile, so no worker ever mixes two versions.
- **A failed publish must leave the running version untouched**, and say what went wrong in a way an assistant can act on.
- **The plugin stays independent.** `apps` talks to the plugin only through its config struct and the three exported functions (`ObserveWorkerOutput`, `LoadedDynamicApps`, `ReloadWorkingDir`).
- When a tool or flag changes, update the pages in `docs/docs/apps/` (tools in `assistant.md`, flags and settings in `running.md`) and `cmd/cli/README.md`. The end-to-end test is [`tests/apps_e2e/main_test.py`](tests/apps_e2e/main_test.py).

## SQL in the `apps` package

The `apps/` package keeps **no SQL in Go**. Schema and queries are plain files under [`apps/db/`](apps/db/), and the Go code that runs queries is generated by [sqlc](https://sqlc.dev):

| Directory | Contents |
|-----------|----------|
| `apps/db/bootstrap/` | Idempotent bookkeeping applied on every start |
| `apps/db/migrations/` | The platform's schema, one numbered file per change. Never edit a released file — add a new one |
| `apps/db/queries/` | Queries on the platform database → generated into `apps/db/dbgen/` |
| `apps/db/appdb/` | Schema and queries that run inside each app's own database → `apps/db/appdb/appdbgen/` |

**Column types** (maintainer, 2026-11-10): give every text column the narrowest type that fits what it holds, for example `char(64)` for a SHA-256 in hex, `varchar(40)` for a username, `varchar(200)` for a file name. Do not reach for `text` by default; use it only for values that can really be large (a traceback, a pasted certificate), and say so in a comment. Enforce the same limit in Go before the value reaches the database, so people get a plain message and not a database error. This applies to the platform's own tables and to any SQL we write for examples and starter apps.

After changing a migration or a query, regenerate and commit the result (never edit the generated packages by hand):

```bash
cd apps && go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 generate
```

The only SQL built in Go is what cannot be a parameterised query: `CREATE`/`ALTER`/`DROP` of each app's role and database in `apps/postgres.go` (identifiers come from validated slugs), and the statements people's own apps supply.

The tests in `apps/` start a real Postgres from the locally installed programs and skip when Postgres or `python3` is missing (`go test -short` skips them too).

---

## Environment

- **Go 1.26** (see `go.mod`) and **xcaddy**: `go install github.com/caddyserver/xcaddy/cmd/xcaddy@v0.4.6`
- **Python 3.13+** (3.13 recommended). Dev tools: `pip install -r requirements-dev.txt` in a venv.
- Build Caddy with the plugin: `xcaddy build --with github.com/mliezun/caddy-snake=.`
- Each integration test app has its own `tests/<app>/requirements.txt`.

## Integration tests

Docker (CI parity, linux/amd64 container):

```bash
./tests/integration.sh <tool-name> <python-version>
```

Tools: `django`, `django_channels`, `flask`, `fastapi`, `simple_autoreload`, `simple_async`, `simple_cache`, `simple_cluster_cache`, `simple_start_timeout`, `simple_isolation`, `simple_request_body`, `simple_mount_prefix`, `socketio`, `dynamic`, `apps_e2e`
Python versions: `3.13`, `3.13-nogil`, `3.14`, `3.14-nogil`, `3.15`, `3.15-nogil`

Local (faster feedback), shown for Flask; FastAPI is the same and also needs `psutil`:

```bash
cd tests/flask
python3.13 -m venv venv && source venv/bin/activate
pip install -r requirements.txt
CGO_ENABLED=0 xcaddy build --with github.com/mliezun/caddy-snake=../..
./caddy run --config Caddyfile > caddy.log 2>&1 &
timeout 60 bash -c 'while ! grep -q "finished cleaning storage units" caddy.log; do sleep 1; done'
python main_test.py
pkill -f "./caddy" || true
```

Python tracebacks from workers land in `tests/<app>/caddy.log`.

---

## Benchmarks

```bash
docker build -t caddy-snake-bench -f benchmarks/Dockerfile .
docker run --rm -v $(pwd)/benchmarks:/workspace/benchmarks caddy-snake-bench
```

For stable linux/amd64 numbers, `./benchmarks/scaleway_bench.sh` provisions a short-lived Scaleway instance, runs the harness and tears it down (needs the Scaleway CLI, `jq`, `tar` and a project SSH key; see the script for env vars).

After re-running benchmarks, update all of:

1. `README.md` — benchmark table and footnote
2. `docs/docs/benchmarks.md` — results table, methodology and analysis
3. `benchmarks/README.md` — results table
4. `docs/static/img/benchmark_chart.svg` — copy from `benchmarks/benchmark_chart.svg`

---

## Releases

Patch releases use semantic tags `v0.x.y` on `main`. Publishing a release triggers CI to build and attach Linux, macOS and Windows binaries (`.github/workflows/build-binary.yml`, `build-standalone.yml`, `python-build.yml`, `docker-publish.yml`).

1. **Ensure `main` is green**: `gh run list --branch main`.
2. **Choose the next tag**: `gh release list --limit 1`.
3. **Bump the version** in `cmd/cli/pyproject.toml` and `ServerVersion` in `apps/mcp.go` to match the tag (without the `v`), commit and push to `main` before tagging:

```bash
git add cmd/cli/pyproject.toml
git commit -m "Bump caddysnake PyPI version to 0.5.5."
git push origin main
```

4. **Create the GitHub release** (creates the tag on `main` and starts the asset builds):

```bash
gh release create v0.5.5 --target main --title "v0.5.5" --notes "$(cat <<'EOF'
## What's Changed
* Short description by @author in https://github.com/mliezun/caddy-snake/pull/NNN

**Full Changelog**: https://github.com/mliezun/caddy-snake/compare/v0.5.4...v0.5.5
EOF
)"
```

The Windows wheels in `python-build.yml` are marked `experimental` (new in 0.8.0): if one fails, the other wheels are still published, so look at those jobs by hand and remove the flag once a release has shipped them.

5. **Wait for the release workflows** — `Caddy Binary`, `Caddy Standalone`, `Python Build Package` and `Docker Publish` must succeed and assets must appear (`gh release view v0.5.5`). Confirm the version on [PyPI](https://pypi.org/project/caddysnake/).

To republish a tag to PyPI manually, run **Python Build Package** via workflow dispatch on `main` with the tag name.
