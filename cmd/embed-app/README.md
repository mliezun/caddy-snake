# embed-app — Package your Python app as a standalone binary

Produces a single executable that embeds Caddy, caddy-snake, Python, and your application.

## Quick start

```bash
./build.sh /path/to/your-app.zip 3.13
./app  # or whatever your zip was named
```

## Requirements

- App as a `.zip` file (Lambda-style: app code + dependencies at root)
- Go 1.26+, xcaddy, Python 3.x (for the build script)

See the [full documentation](../../docs/docs/embed-app.md) for:

- How to create the app zip (Lambda-style packaging)
- All build options
- Platform and architecture notes

## Local testing

After building, run the test script:

```bash
./test_embed.sh embed-test
```

## Build script usage

```
./build.sh APP_ZIP PYTHON_VERSION [OPTIONS]

Options:
  --app MODULE:VAR      App entry point (default: main:app)
  --server-type TYPE    wsgi or asgi (default: wsgi)
  --output NAME         Output binary name
  --arch ARCH           Target architecture (default: auto-detect)
  --list-arch           List available architectures
```
