FROM ubuntu:22.04

ARG GO_VERSION=1.25.0
ARG PY_VERSION=3.14

COPY scripts/setup-python-and-build.sh /usr/local/bin/setup-python-and-build.sh
RUN chmod +x /usr/local/bin/setup-python-and-build.sh

RUN bash /usr/local/bin/setup-python-and-build.sh \
    --python-version ${PY_VERSION} \
    --go-version ${GO_VERSION} \
    --build-caddy \
    --output-caddy-path /usr/local/bin/caddy &&\
    rm -rf /var/lib/apt/lists/*

CMD ["cp", "/usr/local/bin/caddy", "/output/caddy"]
