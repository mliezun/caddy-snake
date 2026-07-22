import os
import sys

import click


@click.command()
@click.option(
    "--server-type",
    "-t",
    type=click.Choice(["wsgi", "asgi", "esgi"], case_sensitive=False),
    required=True,
    help="Required. The type of server to use: wsgi|asgi|esgi",
)
@click.option(
    "--domain",
    "-d",
    help="Domain name at which to serve the files. Enables HTTPS and sets the listener to the appropriate secure port.",
)
@click.option("--app", "-a", required=True, help="Required. App module to be imported")
@click.option("--listen", "-l", help="The address to which to bind the listener")
@click.option(
    "--workers",
    "-w",
    default="0",
    help="The number of workers to spawn (default: 0, uses CPU count)",
)
@click.option("--python-path", help="Path to the Python interpreter")
@click.option("--working-dir", help="Working directory for the Python app")
@click.option("--venv", help="Path to a Python virtual environment to use")
@click.option(
    "--env-file",
    multiple=True,
    help="Dotenv file loaded into worker env (repeatable; later files override)",
)
@click.option(
    "--env-var",
    multiple=True,
    help="Inline worker env var as NAME=VALUE (repeatable; overrides env-file)",
)
@click.option(
    "--start-timeout",
    help="Wait for worker readiness (default: 120s; use -1 or forever for indefinite)",
)
@click.option("--static-path", help="Path to a static directory to serve: path/to/static")
@click.option(
    "--static-route",
    default="/static",
    help="Route to serve the static directory (default: /static)",
)
@click.option("--debug", is_flag=True, help="Enable debug logs")
@click.option("--access-logs", is_flag=True, help="Enable access logs")
@click.option(
    "--autoreload",
    is_flag=True,
    help="Watch .py files and reload on changes",
)
@click.option(
    "--lifespan",
    type=click.Choice(["on", "off"], case_sensitive=False),
    default="off",
    show_default=True,
    help="Enable ASGI lifespan support (ignored in WSGI/ESGI mode)",
)
@click.option(
    "--runtime",
    help="Worker runtime (wsgi: sync|gevent; esgi: gevent only; asgi: native|uvloop)",
)
def main(
    server_type,
    domain,
    app,
    listen,
    workers,
    python_path,
    working_dir,
    venv,
    env_file,
    env_var,
    start_timeout,
    static_path,
    static_route,
    debug,
    access_logs,
    autoreload,
    lifespan,
    runtime,
):
    """
    A Python WSGI, ASGI, or ESGI server designed for apps and frameworks.

    Python-block options mirror the Caddyfile python directive. CLI-only flags
    cover listen address, HTTPS domain, and static files.

    You can specify a custom socket address using the '--listen' option. You can also specify the number of workers to spawn.

    Providing a domain name with the '--domain' flag enables HTTPS and sets the listener to the appropriate secure port.
    Ensure DNS A/AAAA records are correctly set up if using a public domain for secure connections.
    """
    binary_path = os.path.join(os.path.dirname(__file__), "caddysnake-cli")

    if not os.path.exists(binary_path):
        click.echo(f"caddysnake-cli binary file not found at {binary_path}", err=True)
        sys.exit(1)

    # Build the command arguments
    args = [binary_path, "python-server"]

    # Add all the options that were provided
    if server_type:
        args.extend(["--server-type", server_type])
    if domain:
        args.extend(["--domain", domain])
    if app:
        args.extend(["--app", app])
    if listen:
        args.extend(["--listen", listen])
    if workers:
        args.extend(["--workers", workers])
    if python_path:
        args.extend(["--python-path", python_path])
    if working_dir:
        args.extend(["--working-dir", working_dir])
    if venv:
        args.extend(["--venv", venv])
    for path in env_file:
        args.extend(["--env-file", path])
    for item in env_var:
        args.extend(["--env-var", item])
    if start_timeout:
        # Use equals form so values like -1 are not parsed as separate flags.
        args.append(f"--start-timeout={start_timeout}")
    if static_path:
        args.extend(["--static-path", static_path])
    if static_route:
        args.extend(["--static-route", static_route])
    if debug:
        args.append("--debug")
    if access_logs:
        args.append("--access-logs")
    if autoreload:
        args.append("--autoreload")
    if lifespan:
        args.extend(["--lifespan", lifespan])
    if runtime:
        args.extend(["--runtime", runtime])

    # Execute the binary with the constructed arguments
    os.execv(binary_path, args)


if __name__ == "__main__":
    main()
