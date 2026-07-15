FROM ubuntu:22.04 AS builder

ARG GO_VERSION=1.26.5

RUN export DEBIAN_FRONTEND=noninteractive &&\
    apt-get update -yyqq &&\
    apt-get install -yyqq wget tar &&\
    ARCH=$(dpkg --print-architecture) &&\
    wget https://dl.google.com/go/go${GO_VERSION}.linux-${ARCH}.tar.gz && \
    tar -C /usr/local -xzf go*.linux-${ARCH}.tar.gz && \
    rm go*.linux-${ARCH}.tar.gz &&\
    apt-get clean &&\
    rm -rf /var/lib/apt/lists/*

COPY . /build

ENV PATH=$PATH:/usr/local/go/bin

RUN go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest &&\
    cd /usr/local/bin &&\
    CGO_ENABLED=0 /root/go/bin/xcaddy build --with github.com/mliezun/caddy-snake=/build

FROM ubuntu:22.04

ARG PY_VERSION=3.13

RUN export DEBIAN_FRONTEND=noninteractive &&\
    apt-get update -yyqq &&\
    apt-get install -yyqq wget software-properties-common &&\
    add-apt-repository -y ppa:deadsnakes/ppa &&\
    apt-get update -yyqq &&\
    apt-get install -yyqq python${PY_VERSION}-venv &&\
    ln -sf /usr/bin/python${PY_VERSION} /usr/bin/python &&\
    wget -q https://bootstrap.pypa.io/get-pip.py &&\
    python get-pip.py &&\
    apt-get upgrade -yyqq libssl3 openssl &&\
    apt-get clean &&\
    rm -rf /var/lib/apt/lists/* get-pip.py

COPY --from=builder /usr/local/bin/caddy /usr/local/bin/caddy
