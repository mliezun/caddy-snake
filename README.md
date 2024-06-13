# Caddy Snake 🐍

> [Caddy](https://github.com/caddyserver/caddy) is a powerful, enterprise-ready, open source web server with automatic HTTPS written in Go.

This plugin provides native support for Python apps.

It embeds the Python interpreter inside Caddy and serves requests directly without going through a reverse proxy.

Supports both WSGI and ASGI, which means you can run all types of frameworks like Flask, Django and FastAPI.

## Quickstart

#### Requirements

- Python >= 3.9 + dev files
- C compiler and build tools
- Go >= 1.21 and [Xcaddy](https://github.com/caddyserver/xcaddy)

Install requirements on Ubuntu 24.04:

```
$ sudo apt-get install python3-dev build-essential pkg-config golang
$ go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
```

You can also [build with Docker](#build-with-docker).

#### Example usage: Flask

`main.py`

```python
from flask import Flask

app = Flask(__name__)

@app.route("/hello-world")
def hello():
    return "Hello world!"
```

`Caddyfile`

```Caddyfile
http://localhost:9080 {
    route {
        python {
            module_wsgi "main:app"
        }
    }
}
```

Run:

```
$ pip install Flask
$ CGO_ENABLED=1 xcaddy build --with github.com/mliezun/caddy-snake
$ ./caddy run --config Caddyfile
```

```
$ curl http://localhost:9080/hello-world
Hello world!
```

See how to setup [Hot Reloading](#hot-reloading)

#### Example usage: FastAPI

`main.py`

```python
from fastapi import FastAPI

@app.get("/hello-world")
def hello():
    return "Hello world!"
```

`Caddyfile`

```Caddyfile
http://localhost:9080 {
    route {
        python {
            module_asgi "main:app"
        }
    }
}
```

Run:

```
$ pip install fastapi
$ CGO_ENABLED=1 xcaddy build --with github.com/mliezun/caddy-snake
$ ./caddy run --config Caddyfile
```

```
$ curl http://localhost:9080/hello-world
Hello world!
```

> NOTE: It's possible to enable/disable [lifespan events](https://fastapi.tiangolo.com/advanced/events/) by adding the `lifespan on|off` directive to your Caddy configuration. In the above case the lifespan events are disabled because the directive was omitted.

See how to setup [Hot Reloading](#hot-reloading)

## Use docker image

There are docker images available with the following Python versions: `3.9`, `3.10`, `3.11`, `3.12`

Example usage:

```Dockerfile
FROM ghcr.io/mliezun/caddy-snake:latest-py3.12

WORKDIR /app

# Copy your project into app
COPY . /app

# Caddy snake is already installed and has support for Python 3.12
CMD ["caddy", "run", "--config", "/app/Caddyfile"]
```

### Build with Docker

There's a template file in the project: [builder.Dockerfile](/builder.Dockerfile). It supports build arguments to configure which Python or Go version is desired for the build.

Make sure to use the same Python version as you have installed in your system.

You can copy the contents of the builder Dockerfile and execute the following commands to get your Caddy binary: 

```bash
docker build -f builder.Dockerfile --build-arg PY_VERSION=3.9 -t caddy-snake .
```

```bash
docker run --rm -v $(pwd):/output caddy-snake
```

**NOTE**

It's also possible to provide virtual environments with the following syntax:

```Caddyfile
python {
    module_wsgi "main:app"
    venv "./venv"
}
```

What it does behind the scenes is to append `venv/lib/python3.x/site-packages` to python `sys.path`.

> Disclaimer: Currently, when you provide a venv it gets added to the global `sys.path`, which in consequence
> means all apps have access to those packages.

## Hot reloading

Currently the Python app is not reloaded by the plugin if a file changes. But it is possible to setup using [watchmedo](https://github.com/gorakhargosh/watchdog?tab=readme-ov-file#shell-utilities) to restart the Caddy process.

```bash
# Install on Debian and Ubuntu.
sudo apt-get install python3-watchdog
watchmedo auto-restart -d . -p "*.py" --recursive \
    -- caddy run --config Caddyfile
```

Note that this will restart Caddy when new `.py` files are created. If your venv is in the directory watched by watchmedo, installing packages in the venv will also restart Caddy by modifying `.py` files.

## Dev resources

- [Python C API Docs](https://docs.python.org/3.12/c-api/structures.html)
- [Python C API New Type Tutorial](https://docs.python.org/3/extending/newtypes_tutorial.html)
- [Bjoern WSGI](https://github.com/jonashaag/bjoern/tree/master)
- [WSGO](https://github.com/jonny5532/wsgo/blob/main)
- [embedding-python-in-golang | WSGI Django implementation](https://github.com/spikeekips/embedding-python-in-golang/blob/master/wsgi-django)
- [Apache mod_wsgi](https://github.com/GrahamDumpleton/mod_wsgi)
- [FrankenPHP](https://github.com/dunglas/frankenphp)
- [WSGI Standard PEP 3333](https://peps.python.org/pep-3333/)
- [ASGI Spec](https://asgi.readthedocs.io/en/latest/index.html)

## LICENSE

[MIT License](/LICENSE).
