---
title: Connect an assistant
description: Give your AI assistant the address of the server and what it can do there
sidebar_position: 2
---

# Connect an assistant

Any assistant that can use [MCP](https://modelcontextprotocol.io) tools works: Claude, ChatGPT, Cursor, Claude Code and others. No AI is included.

Give it this address as an MCP server (a "connector"):

```
http://localhost:9080/_apps/mcp
```

With [Claude Code](https://claude.com/claude-code):

```bash
claude mcp add --transport http apps http://localhost:9080/_apps/mcp
```

When [sign-in](sharing.md#logins) is on, the assistant needs an **access key** as the connector's Bearer token. Create one on the **Connect** page of the dashboard, or with `caddysnake apps key create <name>`.

Then ask for something:

> Make an app where the team can sign up for Friday lunch.

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

## Use it from git

If `git` is installed on the server, each app is also a normal git remote, for assistants that work in a terminal:

```bash
git clone http://localhost:9080/_apps/git/friday-lunch.git
# edit, commit…
git push origin main          # publishes
```

Pushing `draft/<name>` publishes that draft (create it first with `create_draft`). Tools and git share one history. With logins on, use any username and the access key as the password.
