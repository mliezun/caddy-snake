---
sidebar_position: 1
---

# Quickstart

Let's discover **Caddy Snake in less than 5 minutes**.

## Getting Started

Get started by **building from source**. You can also [build with Docker](#build-with-docker).

```
CGO_ENABLED=1 xcaddy build --with github.com/mliezun/caddy-snake
```

### What you'll need

- Python >= 3.10 + dev files
- C compiler and build tools
- Go >= 1.21 and [Xcaddy](https://github.com/caddyserver/xcaddy)


### Example usage: FastAPI

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

### Example usage: Flask

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

## Use docker image

There are docker images available with the following Python versions: `3.10`, `3.11`, `3.12`, `3.13`.

Example usage:

```Dockerfile
FROM mliezun/caddy-snake:latest-py3.12

WORKDIR /app

# Copy your project into app
COPY . /app

# Caddy snake is already installed and has support for Python 3.12
CMD ["caddy", "run", "--config", "/app/Caddyfile"]
```

Images are available both in Docker Hub and Github Container Registry:

- [https://hub.docker.com/r/mliezun/caddy-snake](https://hub.docker.com/r/mliezun/caddy-snake)
- [https://github.com/mliezun/caddy-snake/pkgs/container/caddy-snake](https://github.com/mliezun/caddy-snake/pkgs/container/caddy-snake)

### Build with Docker

There's a template file in the project: [builder.Dockerfile](https://github.com/mliezun/caddy-snake/blob/main/builder.Dockerfile). It supports build arguments to configure which Python or Go version is desired for the build.

Make sure to use the same Python version as you have installed in your system.

You can copy the contents of the builder Dockerfile and execute the following commands to get your Caddy binary: 

```bash
docker build -f builder.Dockerfile --build-arg PY_VERSION=3.11 -t caddy-snake .
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

## `working_dir` (optional)

Sets the working directory from which your Python module will be resolved and executed.

By default, Caddy uses its own process working directory (often / when run as a service) to resolve Python module imports. However, this default is not always appropriate, especially in modern project structures such as monorepos or deployment environments where Python applications live in subdirectories that are not importable from the root.

The `working_dir` directive allows you to explicitly define the working directory that will be used for:

- Importing the Python module
- Resolving relative paths (e.g., for configuration files or static assets)
- Ensuring consistent behavior across local development and production (e.g., when run under systemd, which defaults to / unless otherwise configured)

```Caddyfile
python {
    module_wsgi "main:app"
    venv "/var/www/myapp/venv"
    working_dir "/var/www/myapp"
}
```

This example tells Caddy to:

- Load the app object from the main.py module
- Use the virtual environment located at /var/www/myapp/venv
- Switch to /var/www/myapp as the working directory before executing Python code

This behavior is analogous to the "path" setting in NGINX Unit, or manually setting the working directory in a systemd service file. It provides flexibility in organizing your Python applications — especially when working within monorepos or containerized environments — and ensures your app runs with the correct context regardless of where Caddy itself is invoked.


## Hot reloading

Currently the Python app is not reloaded by the plugin if a file changes. But it is possible to setup using [watchmedo](https://github.com/gorakhargosh/watchdog?tab=readme-ov-file#shell-utilities) to restart the Caddy process.

#### Install watchdog
```bash
# Install globally on Debian and Ubuntu.
sudo apt-get install python3-watchdog

# Or install with pip
pip install watchdog
```

#### Autorestart caddy server when dev files change
```bash
watchmedo auto-restart -d . -p "*.py" --recursive \
    -- caddy run --config Caddyfile
```

Note that this will restart Caddy when new `.py` files are created. If your venv is in the directory watched by watchmedo, installing packages in the venv will also restart Caddy by modifying `.py` files.
