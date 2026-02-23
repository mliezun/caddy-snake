---
title: Apps as Standalone Binaries
description: Package your Python app with Caddy Snake and Python into a single, self-contained executable
sidebar_position: 6
---

# Apps as Standalone Binaries

Caddy Snake can produce a **single binary** that includes Caddy, the caddy-snake plugin, a Python interpreter, and your application — similar to [FrankenPHP](https://frankenphp.dev/docs/embed)'s approach for PHP apps.

The result is a self-contained executable you can distribute and run without installing Python, Caddy, or any dependencies on the target system.

## Requirements

- **App zip**: A `.zip` file of your application (Lambda-style, see below)
- **Python version**: 3.10, 3.11, 3.12, 3.13, 3.13-nogil, or 3.14
- **Build environment**: Go 1.26+, xcaddy, Python 3.x (for the build script)

For apps with binary dependencies (NumPy, Pillow, etc.), build the zip on the **target platform** (e.g. Linux if you will run on Linux). We assume you use the correct platform when installing dependencies.

---

## Creating the app zip

Create a `.zip` file using the same approach as [AWS Lambda Python deployment packages](https://docs.aws.amazon.com/lambda/latest/dg/python-package.html): your application code and dependencies at the **root** of the zip.

### Option 1: Using a package directory

```bash
cd myapp
mkdir package
pip install -r requirements.txt -t ./package

# Add dependencies to zip
cd package
zip -r ../app.zip .

# Add your app code
cd ..
zip -g app.zip main.py
# Or add multiple files:
zip -g app.zip main.py mymodule/ static/
```

### Option 2: Using a virtual environment

```bash
cd myapp
python3.13 -m venv venv
source venv/bin/activate
pip install -r requirements.txt

# Zip site-packages and your code
cd venv/lib/python3.13/site-packages
zip -r ../../../../app.zip .
cd ../../../../

zip -g app.zip main.py
```

### Zip structure

Your zip should have a flat structure at the root:

```
app.zip
├── main.py              # Your app entry (e.g. app = Flask(...))
├── mymodule/
│   └── __init__.py
├── flask/
├── werkzeug/
└── ... (other dependencies)
```

The Python import path uses the zip contents as the working directory, so `main:app` refers to `main.py` at the root with an `app` variable.

### Tips

- **Exclude** `.git`, `__pycache__`, `*.pyc`, tests, and other dev files to reduce size
- **Binary dependencies** (C extensions): install on the same OS/architecture as your target. Use Docker to build for Linux if you develop on macOS
- **Entry point**: Ensure your app variable matches what you pass to `--app` (default: `main:app`)

---

## Building the standalone binary

Use the `build.sh` script in `cmd/embed-app/`:

```bash
cd cmd/embed-app
./build.sh /path/to/app.zip 3.13
```

This produces a binary named after your zip (e.g. `app`) that you can run directly:

```bash
./app
```

### Build options

| Option | Description | Default |
|--------|-------------|---------|
| `--app MODULE:VAR` | App entry point | `main:app` |
| `--server-type TYPE` | `wsgi` or `asgi` | `wsgi` |
| `--output NAME` | Output binary name | basename of zip |
| `--arch ARCH` | Target architecture | auto-detected |
| `--list-arch` | List available architectures | — |

### Examples

```bash
# Basic build (WSGI, main:app)
./build.sh ./app.zip 3.13

# ASGI app with custom entry point
./build.sh ./app.zip 3.12 --app api:application --server-type asgi

# Custom output name
./build.sh ./deployment.zip 3.13 --output my-service

# Build for a different architecture (e.g. Linux from macOS)
./build.sh ./app.zip 3.13 --arch x86_64-unknown-linux-gnu
```

List available architectures:

```bash
./build.sh /dev/null 3.13 --list-arch
```

---

## Running the binary

```bash
./myapp
```

By default it serves on `:9080`. You can pass [python-server options](reference.md#python-server-command):

```bash
./myapp --listen :8080
./myapp --domain example.com --workers 4
./myapp --static-path ./static --static-route /static
```

---

## Complete example

```bash
# 1. Create your app
mkdir -p myapp && cd myapp
echo 'from flask import Flask
app = Flask(__name__)
@app.route("/")
def hello():
    return "Hello from embedded app!"
' > main.py
echo "flask" > requirements.txt

# 2. Create the zip (Lambda-style)
pip install -r requirements.txt -t package
cd package && zip -r ../app.zip . && cd ..
zip -g app.zip main.py

# 3. Build the standalone binary
cd /path/to/caddy-snake/cmd/embed-app
./build.sh /path/to/myapp/app.zip 3.13 --output myapp

# 4. Run
./myapp
# Visit http://localhost:9080
```

---

## How it works

The build script:

1. Fetches a [python-build-standalone](https://github.com/astral-sh/python-build-standalone) distribution for your chosen Python version
2. Builds Caddy with the caddy-snake plugin via xcaddy
3. Compiles a Go wrapper that embeds: the Caddy binary, the Python distribution, and your app zip

At runtime, the binary extracts all three to temporary directories, sets `PYTHONHOME`, and runs `caddy python-server` with your app as the working directory. The temp directories are removed on exit.
