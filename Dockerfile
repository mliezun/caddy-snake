FROM ubuntu:22.04

ARG GO_VERSION=1.25.0
ARG PY_VERSION=3.13
ARG TARGETARCH

RUN export DEBIAN_FRONTEND=noninteractive &&\
    apt-get update -yyqq &&\
    apt-get install -yyqq wget tar software-properties-common gcc pkgconf &&\
    add-apt-repository -y ppa:deadsnakes/ppa &&\
    apt-get update -yyqq &&\
    apt-get install -yyqq python${PY_VERSION}-dev python${PY_VERSION}-venv &&\
    if [ "$TARGETARCH" = "amd64" ]; then \
        if [ -f /usr/lib/x86_64-linux-gnu/pkgconfig/python-${PY_VERSION}-embed.pc ]; then \
            mv /usr/lib/x86_64-linux-gnu/pkgconfig/python-${PY_VERSION}-embed.pc /usr/lib/x86_64-linux-gnu/pkgconfig/python3-embed.pc; \
        fi &&\
        GO_ARCH=amd64; \
    elif [ "$TARGETARCH" = "arm64" ]; then \
        if [ -f /usr/lib/aarch64-linux-gnu/pkgconfig/python-${PY_VERSION}-embed.pc ]; then \
            mv /usr/lib/aarch64-linux-gnu/pkgconfig/python-${PY_VERSION}-embed.pc /usr/lib/aarch64-linux-gnu/pkgconfig/python3-embed.pc; \
        fi &&\
        GO_ARCH=arm64; \
    fi &&\
    ln -s /usr/bin/python${PY_VERSION} /usr/bin/python &&\
    wget https://bootstrap.pypa.io/get-pip.py &&\
    python get-pip.py &&\
    apt-get clean &&\
    rm -rf /var/lib/apt/lists/* get-pip.py &&\
    wget https://dl.google.com/go/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz && \
    tar -C /usr/local -xzf go*.linux-${GO_ARCH}.tar.gz && \
    rm go*.linux-${GO_ARCH}.tar.gz

COPY . /build

ENV PATH=$PATH:/usr/local/go/bin

RUN go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest &&\
    cd /usr/local/bin &&\
    CGO_ENABLED=1 /root/go/bin/xcaddy build --with github.com/mliezun/caddy-snake=/build &&\
    rm -rf /build
