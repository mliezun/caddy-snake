---
slug: embed-python
title: Caddy with an embedded python distribution
authors: mliezun
tags: [python, docs, caddy-snake, go, web]
---


Now you can retrieve a precompiled binary of caddy with caddy-snake and your preferred version of Python.

Check out the [latest release of caddy-snake](https://github.com/mliezun/caddy-snake/releases).

The following python versions are available: 3.10, 3.11, 3.12, 3.13 and 3.13+freethreading (nogil).

After downloading the precompiled binary you can do:

```bash
$ caddy python-server --app main:app --server-type wsgi
```

This will automatically start a caddy server that serves your WSGI app located in `main:app` Python module.

See `caddy python-server --help` for more instructions.

