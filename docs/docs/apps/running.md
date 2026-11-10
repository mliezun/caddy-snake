---
title: Run it for real
description: Install it as a service, the settings file, Docker, Windows and every option
sidebar_position: 5
---

# Run it for real

## Install

| System | How |
|--------|-----|
| Linux, macOS | `pip install caddysnake` |
| Windows | `pip install caddysnake` (from version 0.8.0), or `caddy-windows-*.zip` from a [release](https://github.com/mliezun/caddy-snake/releases), run as `caddy.exe python-apps` |
| Docker | [Run with Docker](#run-with-docker) |

The Postgres programs must be installed (macOS: `brew install postgresql@18`; Debian/Ubuntu: `sudo apt install postgresql`; Fedora/Rocky: `sudo dnf install postgresql-server`; Windows: the installer from [postgresql.org](https://www.postgresql.org/download/windows/)), unless you pass `--database-url`.

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

`caddy python-apps` builds a Caddy configuration with two handlers in one route: `python_apps` (the dashboard, tools and the gate in front of the apps) followed by `python` with [`module_auto`](../reference.md#module_auto), [`mount_prefix`](../reference.md#mount_prefix), [`memory_limit`](../reference.md#memory_limit) and paths keyed on the first path segment. Builds with only `--with github.com/mliezun/caddy-snake` do not include the platform or its dependencies.
