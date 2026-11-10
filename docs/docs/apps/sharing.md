---
title: Share it with your team
description: Who can reach the server, sign-in, access keys and what is protected
sidebar_position: 4
---

# Share it with your team

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
