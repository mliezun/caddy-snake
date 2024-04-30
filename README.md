# Caddy Snake 🐍

> [Caddy](https://github.com/caddyserver/caddy) is a powerful, enterprise-ready, open source web server with automatic HTTPS written in Go.

This plugin provides native support for Python apps.

It embeds the Python interpreter inside Caddy and serves requests directly without going through a reverse proxy.

It supports both WSGI and ASGI, which means you can run all types of frameworks like Flask, Django and FastAPI.

## Docker image

There's a docker image available, it ships Python 3.12 and can be used as follows:

```Dockerfile
FROM ghcr.io/mliezun/caddy-snake:main

WORKDIR /app

# Copy your project into app
COPY . /app

# Caddy snake is already installed and has support for Python 3.12
CMD ["caddy", "run", "--config", "/app/Caddyfile"]
```

## Build from source

Go 1.21 and Python 3.9 or later is required, with development files to embed the interpreter.

To install in Ubuntu do:

```bash
sudo apt-get update
sudo apt-get install -y python3-dev
```

To install in macOS do:

```bash
brew install python@3
```

### Bundling with Caddy

Build this module using [xcaddy](https://github.com/caddyserver/xcaddy):

```bash
CGO_ENABLED=1 xcaddy build --with github.com/mliezun/caddy-snake@v0.0.5
```

### Build with Docker (or Podman)

There's a template file in the project: [builder.Dockerfile](/builder.Dockerfile). It supports build arguments to configure which Python or Go version is desired for the build.

```Dockerfile
FROM ubuntu:22.04

ARG GO_VERSION=1.22.1
ARG PY_VERSION=3.12

RUN export DEBIAN_FRONTEND=noninteractive &&\
    apt-get update -yyqq &&\
    apt-get install -yyqq wget tar software-properties-common gcc pkgconf &&\
    add-apt-repository -y ppa:deadsnakes/ppa &&\
    apt-get update -yyqq &&\
    apt-get install -yyqq python${PY_VERSION}-dev &&\
    mv /usr/lib/x86_64-linux-gnu/pkgconfig/python-${PY_VERSION}-embed.pc /usr/lib/x86_64-linux-gnu/pkgconfig/python3-embed.pc &&\
    rm -rf /var/lib/apt/lists/* &&\
    wget https://dl.google.com/go/go${GO_VERSION}.linux-amd64.tar.gz && \
    tar -C /usr/local -xzf go*.linux-amd64.tar.gz && \
    rm go*.linux-amd64.tar.gz

ENV PATH=$PATH:/usr/local/go/bin

RUN go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest &&\
    cd /usr/local/bin &&\
    CGO_ENABLED=1 /root/go/bin/xcaddy build --with github.com/mliezun/caddy-snake &&\
    rm -rf /build

CMD ["cp", "/usr/local/bin/caddy", "/output/caddy"]
```

You can copy the contents of the builder Dockerfile and execute the following commands to get your Caddy binary: 

```bash
docker build -f builder.Dockerfile --build-arg PY_VERSION=3.9 -t caddy-snake .
```

```bash
docker run --rm -v $(pwd):/output caddy-snake
```

## Example Caddyfile

```Caddyfile
{
    http_port 9080
    https_port 9443
    log {
        level error
    }
}
localhost:9080 {
    route {
        python "simple_app:main"
    }
}
```

The `python` rule is an HTTP handler that expects a WSGI app as an argument.

If you want to use an ASGI app, like FastAPI or other async frameworks you can use the following config:

```Caddyfile
{
    http_port 9080
    https_port 9443
    log {
        level error
    }
}
localhost:9080 {
    route {
        python {
            module_asgi "example_fastapi:app"
        }
    }
}
```

## Examples

- [simple_app](/examples/simple_app.py). WSGI App that returns the standard hello world message and a UUID.
- [simple_exception](/examples/simple_exception.py). WSGI App that always raises an exception.
- [example_flask](/examples/example_flask.py). Flask application that also returns hello world message and a UUID.
- [example_fastapi](/examples/example_fastapi.py). FastAPI application that also returns hello world message and a UUID.
- [Caddyfile](/examples/Caddyfile). Caddy config that uses all of the example apps.

**NOTE**

It's also possible to provide virtual environments with the following syntax:

```Caddyfile
python {
    module_wsgi "simple_app:main"
    venv_path "./venv"
}
```

What it does behind the scenes is to append `venv/lib/python3.x/site-packages` to python `sys.path`.

> Disclaimer: Currently, when you provide a venv it gets added to the global `sys.path`, which in consequence
> means all apps have access to those packages.

## Dev resources

- [Python C API Docs](https://docs.python.org/3.12/c-api/structures.html)
- [Python C API New Type Tutorial](https://docs.python.org/3/extending/newtypes_tutorial.html)
- [Bjoern WSGI](https://github.com/jonashaag/bjoern/tree/master)
- [WSGO](https://github.com/jonny5532/wsgo/blob/main)
- [embedding-python-in-golang | WSGI Django implementation](https://github.com/spikeekips/embedding-python-in-golang/blob/master/wsgi-django)
- [Apache mod_wsgi](https://github.com/GrahamDumpleton/mod_wsgi)
- [FrankenPHP](https://github.com/dunglas/frankenphp)
- [WSGI Standard PEP 3333](https://peps.python.org/pep-3333/)

## LICENSE

[MIT License](/LICENSE).
