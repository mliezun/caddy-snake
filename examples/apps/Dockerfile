# caddy-snake apps in one container: the server, the Postgres programs it uses
# for its built-in database, git and uv. Everything it stores lives in /data.
#
#   docker compose up        (from this directory)
#
# Needs a caddy-snake image that includes the apps platform (v0.7.0 or newer).
ARG BASE_IMAGE=mliezun/caddy-snake:latest-py3.13
FROM ${BASE_IMAGE}

USER root
RUN export DEBIAN_FRONTEND=noninteractive && \
    apt-get update -yyqq && \
    apt-get install -yyqq --no-install-recommends postgresql postgresql-client git ca-certificates && \
    apt-get clean && rm -rf /var/lib/apt/lists/* && \
    python -m pip install --no-cache-dir --break-system-packages uv && \
    mkdir -p /data && chown caddy:caddy /data

# The built-in Postgres refuses to run as root, which is what we want anyway.
USER caddy
ENV HOME=/home/caddy
VOLUME /data
EXPOSE 9080

# Inside a container the server has to listen on all addresses to be reachable
# through the published port. compose.yaml publishes that port on the host's
# loopback only, so the server is still limited to this computer. To serve other
# people, add --require-login (see the docs) before publishing it more widely.
CMD ["caddy", "python-apps", "--listen", "0.0.0.0:9080", "--open-to-network", \
     "--data-dir", "/data", "--public-url", "http://localhost:9080"]
