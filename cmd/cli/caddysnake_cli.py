import os
import subprocess
import sys
import sysconfig

import click


@click.command()
@click.option(
    "--server-type",
    "-t",
    type=click.Choice(["wsgi", "asgi", "auto"], case_sensitive=False),
    required=True,
    help="Required. The type of server to use: wsgi|asgi|auto",
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
    help="Enable ASGI lifespan support (ignored in WSGI mode)",
)
@click.option(
    "--runtime",
    help="Worker runtime (wsgi: sync|gevent; asgi: native|uvloop)",
)
@click.option(
    "--isolation",
    type=click.Choice(["none", "docker"], case_sensitive=False),
    help="Worker isolation backend: none (default) or docker",
)
@click.option("--isolation-image", help="Docker image when --isolation docker")
@click.option("--isolation-network", help="Docker network for isolated workers")
@click.option("--isolation-docker-host", help="DOCKER_HOST for the Docker CLI")
@click.option("--isolation-memory", help="Docker memory limit (e.g. 512m)")
@click.option("--isolation-cpus", help="Docker CPU limit (e.g. 1.0)")
@click.option(
    "--isolation-read-only",
    is_flag=True,
    help="Mount isolated container root filesystem read-only",
)
@click.option(
    "--cache-mode",
    type=click.Choice(["local", "cluster"], case_sensitive=False),
    help="Cache topology: local (default) or cluster",
)
@click.option("--cache-listen", help="TCP host:port for cluster peer RPC")
@click.option("--cache-advertise", help="Dialable host:port identifying this cluster peer")
@click.option("--cache-peer", multiple=True, help="Static cluster peer host:port (repeatable)")
@click.option("--cache-namespace", help="Cluster cache namespace shared by all peers")
@click.option("--cache-secret", help="Cluster peer shared secret (16-4096 bytes)")
@click.option("--max-dynamic-apps", help="Max distinct dynamic Python apps (default: 128)")
@click.option(
    "--request-body-max-size",
    help="Maximum HTTP request body size (e.g. 1KiB, 2GB). Empty means unlimited.",
)
@click.option(
    "--mount-prefix",
    help="URL path prefix the app is mounted under (e.g. /tools/inventory).",
)
@click.option(
    "--memory-limit",
    help="Memory ceiling per dynamically loaded app (e.g. 256MiB); needs placeholders.",
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
    isolation,
    isolation_image,
    isolation_network,
    isolation_docker_host,
    isolation_memory,
    isolation_cpus,
    isolation_read_only,
    cache_mode,
    cache_listen,
    cache_advertise,
    cache_peer,
    cache_namespace,
    cache_secret,
    max_dynamic_apps,
    request_body_max_size,
    mount_prefix,
    memory_limit,
):
    """
    A Python WSGI or ASGI server designed for apps and frameworks.

    Python-block options mirror the Caddyfile python directive. CLI-only flags
    cover listen address, HTTPS domain, and static files.

    You can specify a custom socket address using the '--listen' option. You can also specify the number of workers to spawn.

    Providing a domain name with the '--domain' flag enables HTTPS and sets the listener to the appropriate secure port.
    Ensure DNS A/AAAA records are correctly set up if using a public domain for secure connections.
    """
    binary_path = _binary_path()

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
    if isolation:
        args.extend(["--isolation", isolation])
    if isolation_image:
        args.extend(["--isolation-image", isolation_image])
    if isolation_network:
        args.extend(["--isolation-network", isolation_network])
    if isolation_docker_host:
        args.extend(["--isolation-docker-host", isolation_docker_host])
    if isolation_memory:
        args.extend(["--isolation-memory", isolation_memory])
    if isolation_cpus:
        args.extend(["--isolation-cpus", isolation_cpus])
    if isolation_read_only:
        args.append("--isolation-read-only")
    if cache_mode:
        args.extend(["--cache-mode", cache_mode])
    if cache_listen:
        args.extend(["--cache-listen", cache_listen])
    if cache_advertise:
        args.extend(["--cache-advertise", cache_advertise])
    for peer in cache_peer:
        args.extend(["--cache-peer", peer])
    if cache_namespace:
        args.extend(["--cache-namespace", cache_namespace])
    if cache_secret:
        args.extend(["--cache-secret", cache_secret])
    if max_dynamic_apps:
        args.extend(["--max-dynamic-apps", max_dynamic_apps])
    if request_body_max_size:
        args.extend(["--request-body-max-size", request_body_max_size])
    if mount_prefix:
        args.extend(["--mount-prefix", mount_prefix])
    if memory_limit:
        args.extend(["--memory-limit", memory_limit])

    # Execute the binary with the constructed arguments
    if _WINDOWS:
        _run_and_exit(args)
    os.execv(binary_path, args)


# Subcommands of `caddysnake apps` that are not "start the server".
_APPS_SUBCOMMANDS = {"user", "key", "call", "guide", "install", "uninstall", "help", "completion"}


_WINDOWS = os.name == "nt"


def _binary_path() -> str:
    name = "caddysnake-cli.exe" if _WINDOWS else "caddysnake-cli"
    return os.path.join(os.path.dirname(__file__), name)


def _run_and_exit(args: list[str], env: dict[str, str] | None = None) -> None:
    """Run the bundled binary and exit with its exit code.

    Windows cannot replace the running program with another one the way
    os.execv does elsewhere: the shell would get its prompt back while the
    server kept running. So the binary runs as a child and this waits for it.
    """
    proc = subprocess.Popen(args, env=env)  # noqa: S603 - our own bundled binary
    while True:
        try:
            sys.exit(proc.wait())
        except KeyboardInterrupt:
            # Ctrl-C reaches the server as well; keep waiting while it stops cleanly.
            continue


def _tool_dirs() -> list[str]:
    """Folders where this Python installation keeps programs such as uv."""
    dirs = [os.path.dirname(sys.executable)]
    scripts = sysconfig.get_path("scripts")
    if scripts and scripts not in dirs:
        dirs.append(scripts)
    return dirs


def apps_argv(argv: list[str]) -> list[str]:
    """Build the argument list for `caddysnake apps ...` (everything after "apps").

    The bundled binary's `python-apps` command takes the arguments unchanged.
    When starting the server, apps are built with the Python that caddysnake
    itself is installed in, unless the caller chose one.
    """
    args = [_binary_path(), "python-apps", *argv]
    starts_server = not any(a in _APPS_SUBCOMMANDS for a in argv if not a.startswith("-"))
    wants_help = any(a in ("-h", "--help") for a in argv)
    has_python = any(a == "--python-path" or a.startswith("--python-path=") for a in argv)
    if starts_server and not wants_help and not has_python:
        args += ["--python-path", sys.executable]
    return args


def cli() -> None:
    """Entry point: `caddysnake apps ...` runs the apps platform, anything else the server."""
    if len(sys.argv) > 1 and sys.argv[1] == "apps":
        binary_path = _binary_path()
        if not os.path.exists(binary_path):
            click.echo(f"caddysnake-cli binary file not found at {binary_path}", err=True)
            sys.exit(1)
        # uv is installed next to this interpreter (it is a dependency); make sure
        # the binary finds it even when the environment is not activated.
        env = dict(os.environ)
        env["PATH"] = os.pathsep.join([*_tool_dirs(), env.get("PATH", "")])
        if _WINDOWS:
            _run_and_exit(apps_argv(sys.argv[2:]), env)
        # The wrapper's whole job is to pass its arguments to the bundled binary.
        # nosemgrep: python.lang.security.audit.dangerous-os-exec-tainted-env-args.dangerous-os-exec-tainted-env-args
        os.execve(binary_path, apps_argv(sys.argv[2:]), env)
        return
    main()


if __name__ == "__main__":
    cli()
