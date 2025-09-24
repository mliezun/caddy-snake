---
slug: docs-release
title: Docs Release
authors: mliezun
tags: [python, docs, caddy-snake, go, web]
---

Releasing docs for caddy-snake a plugin provides native support for Python apps.

It uses the Python C API to run applications directly inside Caddy, avoiding the need for an extra layer of HTTP proxy.

Supports both WSGI and ASGI, which means you can run all types of frameworks like Flask, Django and FastAPI.

<!-- truncate -->

## Why Caddy Snake?

Caddy Snake simplifies the deployment of Python web applications by eliminating the need for intermediary tools like Gunicorn or Daphne. By integrating tightly with Caddy, it:

- Reduces complexity in your deployment stack.

- Improves performance by cutting out unnecessary hops.

- Offers seamless integration with Caddy's powerful features like automatic HTTPS and dynamic configuration.


See how to get started in [Quickstart](../docs/intro).
