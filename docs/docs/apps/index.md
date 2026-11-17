---
title: Apps
description: Build and share small internal web apps with your own AI assistant
sidebar_position: 1
---

# Apps

`caddysnake apps` turns one machine into a place where people build and share small internal web apps **through their own AI assistant**. The assistant connects over [MCP](https://modelcontextprotocol.io), writes the code, and publishes it; the person gets an address to open and share.

- Each app is served at `/<app-name>/` and has **its own database**, a single file. No database server to install or run.
- Every save is a **version**. A broken version is refused and the running one stays up. You can go back to any earlier version.
- A **draft** is a private copy of an app, with a copy of its data, for trying changes safely.
- No AI is included: bring any assistant that can use MCP tools (Claude, ChatGPT, Cursor, Claude Code, …).

It is one program: Caddy, the caddy-snake plugin and the platform. Everything it stores is in one folder.

![An assistant is asked for an app; the app appears in the dashboard and opens](/img/apps-demo.gif)

## Quick start

You need Python 3.13 or newer. There is nothing else to install: each app keeps its data in a file the server looks after.

### 1. Start the server

```bash
pip install caddysnake
caddysnake apps
```

```text
caddy-snake apps is running.
  Open:               http://localhost:9080/
  Assistant (MCP):    http://localhost:9080/_apps/mcp
  Data folder:        ~/.caddysnake/apps
```

### 2. Connect your assistant

Add the assistant address to your assistant as an MCP server (a "connector"):

```text
http://localhost:9080/_apps/mcp
```

With [Claude Code](https://claude.com/claude-code), for instance:

```bash
claude mcp add --transport http apps http://localhost:9080/_apps/mcp
```

### 3. Ask for an app

> Make an app where the team can sign up for Friday lunch.

The assistant creates the app and tells you its address, for example `http://localhost:9080/friday-lunch/`. The page at `http://localhost:9080/` lists every app.

:::tip Prefer Docker?
See [Run with Docker](running.md#run-with-docker).
:::

## Where to go next

| | |
|---|---|
| [Connect an assistant](assistant.md) | The address to give it, what it can do, access keys |
| [How apps work](how-apps-work.md) | Versions, drafts, databases, what happens on publish |
| [Share it with your team](sharing.md) | Sign-in, private networks, HTTPS |
| [Run it for real](running.md) | As a service, settings file, Docker, Windows, every option |

Looking for the Caddy plugin that runs Python apps from a Caddyfile? It is [here](../intro.md).
