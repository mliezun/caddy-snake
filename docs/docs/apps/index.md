---
title: Apps
description: Build and share small internal web apps with your own AI assistant
sidebar_position: 1
---

# Apps

`caddysnake apps` turns one machine into a place where people build and share small internal web apps **through their own AI assistant**. The assistant connects over [MCP](https://modelcontextprotocol.io), writes the code, and publishes it; the person gets an address to open and share.

- Each app is served at `/<app-name>/` and has **its own Postgres database**.
- Every save is a **version**. A broken version is refused and the running one stays up. You can go back to any earlier version.
- A **draft** is a private copy of an app, with a copy of its data, for trying changes safely.
- No AI is included: bring any assistant that can use MCP tools (Claude, ChatGPT, Cursor, Claude Code, …).

It is one program: Caddy, the caddy-snake plugin and the platform, with a database it manages for you.

![An assistant is asked for an app; the app appears in the dashboard and opens](/img/apps-demo.gif)

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

Prefer Docker? See [Run with Docker](running.md#run-with-docker).

## Where to go next

| | |
|---|---|
| [Connect an assistant](assistant.md) | The address to give it, what it can do, access keys |
| [How apps work](how-apps-work.md) | Versions, drafts, databases, what happens on publish |
| [Share it with your team](sharing.md) | Sign-in, private networks, HTTPS |
| [Run it for real](running.md) | As a service, settings file, Docker, Windows, every option |

Looking for the Caddy plugin that runs Python apps from a Caddyfile? It is [here](../intro.md).
