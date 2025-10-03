import os
import sys

import click


@click.command()
@click.option(
    "--server-type",
    "-t",
    type=click.Choice(["wsgi", "asgi"], case_sensitive=False),
    required=True,
    help="Required. The type of server to use: wsgi|asgi",
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
@click.option(
    "--workers-runtime",
    "-r",
    type=click.Choice(["thread", "process"], case_sensitive=False),
    default="process",
    help="The runtime to use for the workers: thread|process",
)
@click.option(
    "--static-path", help="Path to a static directory to serve: path/to/static"
)
@click.option(
    "--static-route",
    default="/static",
    help="Route to serve the static directory (default: /static)",
)
@click.option("--debug", is_flag=True, help="Enable debug logs")
@click.option("--access-logs", is_flag=True, help="Enable access logs")
def main(
    server_type,
    domain,
    app,
    listen,
    workers,
    workers_runtime,
    static_path,
    static_route,
    debug,
    access_logs,
):
    """
    A Python WSGI or ASGI server designed for apps and frameworks.

    You can specify a custom socket address using the '--listen' option. You can also specify the number of workers to spawn and the runtime to use for the workers.

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
    if workers_runtime:
        args.extend(["--workers-runtime", workers_runtime])
    if static_path:
        args.extend(["--static-path", static_path])
    if static_route:
        args.extend(["--static-route", static_route])
    if debug:
        args.append("--debug")
    if access_logs:
        args.append("--access-logs")

    # Execute the binary with the constructed arguments
    os.execv(binary_path, args)
