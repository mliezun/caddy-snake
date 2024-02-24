# Caddy Snake 🐍

Caddy plugin that gives native support for Python WSGI apps.

It embeds the Python interpreter inside Caddy and serves requests directly without going through a reverse proxy or creating a new process.

## Install

Go 1.21 and Python 3.9 or later is required, with development files to embed the interpreter.

To install in Ubuntu do:

```bash
sudo apt-get update
sudo apt-get install -y python3-dev
```

To install in MacOS do:

```bash
brew install python@3
```

### Bundling with Caddy

Build this module using [xcaddy](https://github.com/caddyserver/xcaddy):

```bash
CGO_ENABLED=1 xcaddy build --with github.com/mliezun/caddy-snake@v0.0.4
```

### Use Docker to build (or Podman)

Dockerfile:

```Dockerfile
FROM python:3.12

WORKDIR /root

RUN apt-get update -y &&\
    wget https://go.dev/dl/go1.21.6.linux-amd64.tar.gz &&\
    tar -xvf go1.21.6.linux-amd64.tar.gz &&\
    rm -rf go1.21.6.linux-amd64.tar.gz &&\
    mv go /usr/local &&\
    cp /usr/lib/x86_64-linux-gnu/pkgconfig/python-3.12-embed.pc /usr/lib/x86_64-linux-gnu/pkgconfig/python3-embed.pc
ENV GOROOT=/usr/local/go
ENV GOPATH=$HOME/go
ENV PATH=$GOPATH/bin:$GOROOT/bin:$PATH
RUN go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
RUN CGO_ENABLED=1 xcaddy build --with github.com/mliezun/caddy-snake@v0.0.4

# Use caddy binary located in: /root/caddy
```

* `docker build -f Dockerfile -t caddy-snake`
* `docker cp caddy-snake:/root/caddy ./caddy`

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

The `python` rule is an HTTP handler that expects a wsgi app as an argument.

## Examples

- [simple_app](/examples/simple_app.py). WSGI App that returns the standard hello world message and a UUID.
- [example_flask](/examples/example_flask.py). Flask application that also returns hello workd message and a UUID.

**NOTE**

At the moment there's no support for virtual environments. To use one is necessary to set the `PYTHONPATH` env variable when starting caddy. As follows:

```bash
PYTHONPATH="venv/lib/python3.12/site-packages/" caddy run --config Caddyfile
```

Make sure to use the right python version depending on your case ^.

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
