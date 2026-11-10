---
title: Apps platform
description: Let people build and share small internal web apps with their own AI assistant
sidebar_position: 3
---

# Apps platform

`caddysnake apps` turns one machine into a place where people build and share small internal web apps **through their own AI assistant**. The assistant connects over [MCP](https://modelcontextprotocol.io), writes the code, and publishes it; the person gets an address to open and share.

- Each app is served at `/<app-name>/` and has **its own Postgres database**.
- Every save is a **version**. A broken version is refused and the running one stays up. You can go back to any earlier version.
- A **draft** is a private copy of an app, with a copy of its data, for trying changes safely.
- No AI is included: bring any assistant that can use MCP tools (Claude, ChatGPT, Cursor, Claude Code, …).

It is one program: Caddy, the caddy-snake plugin and the platform, with a database it manages for you.

## Quick start

You need Python 3.12+ and the Postgres programs installed (macOS: `brew install postgresql@18`; Debian/Ubuntu: `sudo apt install postgresql`; Fedora/Rocky: `sudo dnf install postgresql-server`; Windows: the installer from [postgresql.org](https://www.postgresql.org/download/windows/)). The server starts its own private database; it does not touch one you already run.

```bash
pip install caddysnake
caddysnake apps
```

```
caddy-snake apps is running.
  Open:               http://localhost:9080/
  Assistant (MCP):    http://localhost:9080/_apps/mcp
  Data folder:        ~/.caddysnake/apps
```

Add `http://localhost:9080/_apps/mcp` to your assistant as an MCP server (a "connector"), then ask it for something:

> Make an app where the team can sign up for Friday lunch.

The assistant creates the app, and tells you its address — for example `http://localhost:9080/friday-lunch/`. The page at `http://localhost:9080/` lists every app.

With [Claude Code](https://claude.com/claude-code), for instance:

```bash
claude mcp add --transport http apps http://localhost:9080/_apps/mcp
```

Prefer Docker? See [Run with Docker](#run-with-docker). On Windows, `pip install caddysnake` works from version 0.7.0; each release also has a `caddy-windows-*.zip` to run as `caddy.exe python-apps`.

## The dashboard

The server's own pages, at its root address:

| Page | What is there |
|------|---------------|
| **Apps** | Every app with its address and state |
| An app's page | Versions (with "go back"), drafts, settings, recent activity, remove |
| **Connect** | The assistant address; your access keys when sign-in is on |
| **People** | Add and remove people and set passwords (administrators, when sign-in is on) |

Everything on an app's page is also available to the assistant as a tool. People and access keys are not: only a signed-in person decides who gets in.

## What the assistant can do

The assistant receives instructions on how apps must be built (the same text is printed by `caddysnake apps guide`) and these tools:

| Tool | What it does |
|------|--------------|
| `guide` | How to build apps here and how to talk to the person being helped |
| `list_apps`, `get_app` | What exists, with addresses, drafts, versions and setting names |
| `create_app` | Create an app from a small working starter and publish it |
| `read_files`, `save_files` | Read the files; save changes as a new version and publish it |
| `publish`, `list_versions`, `roll_back` | Put a version live; go back to an earlier one |
| `create_draft`, `publish_draft`, `delete_draft` | Work on a private copy, then make it the live app |
| `get_logs` | Errors with tracebacks, printed output, publish results |
| `set_setting` | Store a secret or setting the app reads as an environment variable |
| `run_sql` | Look at the app's data |
| `get_status` | Which apps are awake and how much memory they use |
| `delete_app` | Remove an app with all its data (asks for the name again) |

The same tools are available without MCP:

```bash
caddysnake apps call list
caddysnake apps call create_app '{"name": "Friday lunch"}'
caddysnake apps call get_logs '{"app": "friday-lunch"}'
```

and over HTTP: `GET /_apps/api/tools` lists them, `POST /_apps/api/tools/<name>` with a JSON body runs one.

## How apps are built

The instructions steer assistants toward apps this platform runs well:

- **Python**, with `main.py` defining `app` — a Flask app, a FastAPI/Starlette app, or any WSGI/ASGI callable. The kind is detected automatically.
- Packages listed in `requirements.txt` are installed on publish (with [uv](https://docs.astral.sh/uv/)).
- **No build step**: plain HTML, vanilla JavaScript and CSS.
- **Relative links.** The app lives under `/<app-name>/`, and the platform passes that prefix to the framework ([`mount_prefix`](reference.md#mount_prefix)), so `url_for`, redirects and static files work. Hand-written absolute links such as `href="/items"` would point outside the app.
- Data in the app's own database, at `DATABASE_URL`. The shape of the data changes only through numbered SQL files in `migrations/`, each applied once, in order, on publish.

Environment variables every app gets: `DATABASE_URL`, `APP_NAME`, `APP_SLUG`, `APP_BASE_PATH`, `APP_URL`, `APP_IS_DRAFT`, plus its own settings.

### What happens on publish

1. The version's files are copied to a fresh folder — the running version is never modified in place.
2. A Python environment is built and `requirements.txt` installed.
3. The app is imported, to check that it starts.
4. New `migrations/*.sql` files are applied to the app's database.
5. The app is switched to the new version. Requests already running finish on the old one.

If any step fails, people using the app notice nothing, and the assistant gets the error (the traceback, the failing SQL statement) so it can fix it and try again. A database change that was already applied in step 4 is kept.

### Drafts

`create_draft` copies an app: its files at the current version, its settings, and its data (with `pg_dump`). The draft is served at `/<app>--<draft>/`. Changes there do not touch the live app.

`publish_draft` makes the draft's files the live app and removes the draft. The draft's **data is discarded**; its migration files run against the live data. If the live app changed after the draft was started, publishing is refused rather than overwriting those changes.

A draft nobody opens or changes for two weeks is removed.

## Who can get in

By default the server listens on this computer only and **anyone who can reach it can use it**, including creating and running apps. That is right for trying it out and for a server reachable only on a trusted private network.

| Situation | How to start it |
|-----------|-----------------|
| Just this computer | `caddysnake apps` |
| A trusted private network (VPN, office network) | `caddysnake apps --listen 0.0.0.0:9080 --open-to-network --public-url http://apps.internal:9080` |
| Anywhere else | `caddysnake apps --listen 0.0.0.0:9080 --require-login --public-url …` |
| With automatic HTTPS | `caddysnake apps --domain apps.example.com --require-login` |

The server refuses to listen beyond this computer unless you pass `--require-login` or `--open-to-network`, because without logins everyone who can reach it can run code on it.

### Logins

With `--require-login`, everyone signs in with a username and password before seeing the list of apps or any app. Everyone who is signed in can open every app.

```bash
caddysnake apps user add ana --admin      # asks for a password
caddysnake apps user list
caddysnake apps user password ana
caddysnake apps user remove ana
```

There is no "forgot password" email: the person running the server sets a new one.

Assistants and scripts use an **access key** in place of a password:

```bash
caddysnake apps key create ana            # prints the key once
caddysnake apps key revoke ana
```

Give the key to the assistant as the connector's Bearer token, or set `CADDYSNAKE_APPS_KEY` for `caddysnake apps call`.

Apps never implement login themselves. When logins are on, each request to an app carries the person's username in the `X-Forwarded-User` header (a value sent by the browser is discarded).

### What protects the dashboard and tools

- They only answer under the host name in `--public-url` (and `localhost`), so another website cannot reach them by pointing its own name at your server.
- Requests made by another website's pages are refused, and tool calls must be JSON.
- Passwords are stored with Argon2id; session tokens and access keys only as hashes.

### What is not protected

Apps are not isolated from each other. They run as the same operating-system user and an app's code can read other apps' files. Treat everyone who may create apps as one team that trusts each other.

## Use it from git

If `git` is installed on the server, each app is also a normal git remote, for assistants that work in a terminal:

```bash
git clone http://localhost:9080/_apps/git/friday-lunch.git
# edit, commit…
git push origin main          # publishes
```

Pushing `draft/<name>` publishes that draft (create it first with `create_draft`). Tools and git share one history. With logins on, use any username and the access key as the password.

## Options

| Flag | Description | Default |
|------|-------------|---------|
| `--config` | [Settings file](#settings-file) | see below |
| `--listen` | Address to listen on | `127.0.0.1:9080` (`:443` with `--domain`) |
| `--domain` | Domain name to serve with automatic HTTPS | — |
| `--public-url` | The address people use to reach the server (links, allowed host name) | from `--listen` / `--domain` |
| `--data-dir` | Folder for app files, history and the built-in database | `~/.caddysnake/apps` |
| `--database-url` | Use an existing Postgres server in place of the built-in one | — |
| `--python-path` | Python used to build each app's environment | the Python `caddysnake` is installed in |
| `--require-login` | Ask everyone to sign in | off |
| `--open-to-network` | Allow listening beyond this computer without logins | off |
| `--max-apps` | How many apps may be awake at once | 128 |
| `--app-memory-limit` | Memory ceiling per app; an app over it is restarted (`""` for none) | `512MiB` |
| `--debug`, `--access-logs` | Logging | off |

Apps that are not used go to sleep after 30 minutes (`CADDYSNAKE_DYNAMIC_APP_IDLE_TTL`) and wake on the next visit, so many small apps fit on one machine.

With `--database-url`, the user in the URL must be allowed to create roles and databases. Copying data into drafts needs `pg_dump` and `psql` on the server, no older than the database; without them drafts start with empty data.

### The data folder

```
~/.caddysnake/apps/
├── apps/<app>/current -> releases/<version>-<time>/   the running version (files + .venv)
├── apps/<app>/settings.env                            what the app runs with
├── repos/<app>.git                                    every version of the app's files
└── postgres/                                          the built-in database
```

Back it up as a whole while the server is stopped, or snapshot the disk it is on.

## Run it as a service

The server can install itself so it starts on its own: with systemd on Linux, launchd on macOS, and as a Windows service.

```bash
caddysnake apps install --domain apps.example.com --require-login
```

This is a **system service** that starts with the machine. It needs administrator rights: on Linux and macOS it asks for your password (it uses `sudo`); on Windows, run it from a terminal opened with "Run as administrator".

| | Linux | macOS | Windows |
|---|-------|-------|---------|
| Service | `caddysnake-apps` (systemd) | `caddysnake.apps` (launchd) | `caddysnake-apps` |
| Runs as | its own account, `caddysnake`, confined to its own folders | you | the built-in account LocalService, which has few rights |
| Settings | `/etc/caddysnake/apps.toml` | `/usr/local/etc/caddysnake/apps.toml` | `%ProgramData%\caddysnake\apps.toml` |
| Data | `/var/lib/caddysnake/apps` | `/usr/local/var/caddysnake/apps` | `%ProgramData%\caddysnake\apps` |
| Programs | copied to `/usr/local/lib/caddysnake` | copied to `/usr/local/lib/caddysnake` | copied to `%ProgramFiles%\caddysnake` |
| Status | `systemctl status caddysnake-apps` | `launchctl print system/caddysnake.apps` | `sc.exe query caddysnake-apps` |
| Output | `journalctl -u caddysnake-apps -f` | `/usr/local/var/log/caddysnake-apps.log` | `%ProgramData%\caddysnake\logs\caddysnake-apps.log` |

Without administrator rights, on Linux and macOS, install it for **your own account**. It then starts when you log in:

```bash
caddysnake apps install --user
```

Settings go to `~/.config/caddysnake/apps.toml` and data to `~/.caddysnake/apps` on both systems.

See everything an install would do, without changing anything:

```bash
caddysnake apps install --dry-run
```

- The flags you pass (`--listen`, `--domain`, `--public-url`, `--require-login`, `--open-to-network`, `--data-dir`, `--database-url`, `--python-path`) are written to the settings file.
- An existing settings file is **never overwritten**. To change something later, edit the file and restart the service (`sudo systemctl restart caddysnake-apps`; on macOS and Windows, run `install` again).
- On Linux and Windows the service cannot read personal folders. If Python is inside one, it uses Python 3.13, which `uv` downloads on first use.
- On Windows the settings, data and logs can be read by administrators and the service only.
- Manage people on an installed server with `caddysnake apps user add <name> --admin` (with `sudo` for a Linux system service, from an administrator terminal on Windows).
- `caddysnake apps uninstall` stops and removes the service and keeps settings and data. `--purge` removes them too, with every app. Add `--user` for a service installed with `--user`.

## Settings file

Everything that can be a flag can be kept in a TOML file:

```toml
listen = "0.0.0.0:9080"
public_url = "http://apps.internal:9080"
require_login = true
data_dir = "/var/lib/caddysnake/apps"
max_apps = 64
app_memory_limit = "256MiB"
```

| Setting | Flag |
|---------|------|
| `listen`, `domain`, `public_url` | `--listen`, `--domain`, `--public-url` |
| `data_dir`, `database_url`, `python_path` | `--data-dir`, `--database-url`, `--python-path` |
| `require_login`, `open_to_network` | `--require-login`, `--open-to-network` |
| `max_apps`, `app_memory_limit` | `--max-apps`, `--app-memory-limit` |
| `debug`, `access_logs` | `--debug`, `--access-logs` |
| `log_file` | `--log-file` |

The file used is the first of: `--config <path>`, `$CADDYSNAKE_APPS_CONFIG`, `~/.config/caddysnake/apps.toml` (or `$XDG_CONFIG_HOME/caddysnake/apps.toml`), then the machine's file: `/etc/caddysnake/apps.toml` on Linux, `/usr/local/etc/caddysnake/apps.toml` on macOS, `%ProgramData%\caddysnake\apps.toml` on Windows. On Windows your own file is `%AppData%\caddysnake\apps.toml`. Flags on the command line override the file. The `user`, `key` commands read it too, so they act on the same data as the server.

A file that cannot be read, or that contains a setting this version does not know, is an error: the server does not start with defaults by accident.

## Run with Docker

[`examples/apps`](https://github.com/mliezun/caddy-snake/tree/main/examples/apps) has a `Dockerfile` and `compose.yaml` that run everything in one container, with the data in a volume:

```bash
cd examples/apps
docker compose up
```

It is published on this computer only (`127.0.0.1:9080`). To serve other people, change the command to `--require-login`, create a user with `docker compose exec apps caddy python-apps user add <name> --admin --data-dir /data`, and publish the port more widely.

## Without the `caddysnake` command

The platform is the Go package `github.com/mliezun/caddy-snake/apps`. Build it into Caddy next to the plugin:

```bash
xcaddy build \
  --with github.com/mliezun/caddy-snake \
  --with github.com/mliezun/caddy-snake/apps
./caddy python-apps
```

`caddy python-apps` builds a Caddy configuration with two handlers in one route: `python_apps` (the dashboard, tools and the gate in front of the apps) followed by `python` with [`module_auto`](reference.md#module_auto), [`mount_prefix`](reference.md#mount_prefix), [`memory_limit`](reference.md#memory_limit) and paths keyed on the first path segment. Builds with only `--with github.com/mliezun/caddy-snake` do not include the platform or its dependencies.
