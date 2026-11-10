---
title: How apps work
description: What an app is made of, versions, drafts and what happens on publish
sidebar_position: 3
---

# How apps work

## How apps are built

The instructions steer assistants toward apps this platform runs well:

- **Python**, with `main.py` defining `app` — a Flask app, a FastAPI/Starlette app, or any WSGI/ASGI callable. The kind is detected automatically.
- Packages are installed on publish with [uv](https://docs.astral.sh/uv/), from `requirements.txt` or from `pyproject.toml` (`[project]` dependencies; a `uv.lock` next to it is followed exactly). With both files, `pyproject.toml` is used.
- **Plain HTML, vanilla JavaScript and CSS are recommended**, not required: nothing is compiled on publish, so an app that needs a build step has to be saved already built.
- **Relative links.** The app lives under `/<app-name>/`, and the platform passes that prefix to the framework ([`mount_prefix`](../reference.md#mount_prefix)), so `url_for`, redirects and static files work. Hand-written absolute links such as `href="/items"` would point outside the app.
- Data in the app's own database, at `DATABASE_URL`. The shape of the data changes only through numbered SQL files in `migrations/`, each applied once, in order, on publish.

Environment variables every app gets: `DATABASE_URL`, `APP_NAME`, `APP_SLUG`, `APP_BASE_PATH`, `APP_URL`, `APP_IS_DRAFT`, plus its own settings.

### What happens on publish

1. The version's files are copied to a fresh folder — the running version is never modified in place.
2. A Python environment is built and the app's packages are installed.
3. The app is imported, to check that it starts.
4. New `migrations/*.sql` files are applied to the app's database.
5. The app is switched to the new version. Requests already running finish on the old one.

If any step fails, people using the app notice nothing, and the assistant gets the error (the traceback, the failing SQL statement) so it can fix it and try again. A database change that was already applied in step 4 is kept.

### Drafts

`create_draft` copies an app: its files at the current version, its settings, and its data (with `pg_dump`). The draft is served at `/<app>--<draft>/`. Changes there do not touch the live app.

`publish_draft` makes the draft's files the live app and removes the draft. The draft's **data is discarded**; its migration files run against the live data. If the live app changed after the draft was started, publishing is refused rather than overwriting those changes.

A draft nobody opens or changes for two weeks is removed.

## The dashboard

The server's own pages, at its root address:

| Page | What is there |
|------|---------------|
| **Apps** | Every app with its address and state |
| An app's page | Versions (with "go back"), drafts, settings, recent activity, remove |
| **Connect** | The assistant address; your access keys when sign-in is on |
| **People** | Add and remove people and set passwords (administrators, when sign-in is on) |

Everything on an app's page is also available to the assistant as a tool. People and access keys are not: only a signed-in person decides who gets in.

## Sleeping apps

Apps that are not used go to sleep after 30 minutes and wake on the next visit, so many small apps fit on one machine. An app that uses more memory than its limit (512 MiB unless you change it) is restarted. See [the options](running.md#options).
