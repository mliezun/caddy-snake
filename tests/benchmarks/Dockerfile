FROM python:3.13-bullseye

# Install go version 1.24
RUN apt-get update && \
    apt-get install -y wget bc wrk git && \
    wget https://go.dev/dl/go1.24.0.linux-arm64.tar.gz && \
    tar -C /usr/local -xzf go1.24.0.linux-arm64.tar.gz && \
    rm go1.24.0.linux-arm64.tar.gz &&\
    pip install fastapi uvicorn hypercorn granian asyncpg pydantic

ENV PATH=$PATH:/usr/local/go/bin
ENV GODEBUG=cgocheck=0

RUN go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest

COPY . /caddy-snake

RUN cd /caddy-snake &&\
    # git apply tests/benchmarks/caddysnake.patch &&\
    cd /usr/local/bin &&\
    CGO_ENABLED=1 /root/go/bin/xcaddy build --with github.com/mliezun/caddy-snake=/caddy-snake

WORKDIR /caddy-snake/tests/benchmarks
