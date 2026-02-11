FROM ubuntu:22.04

ARG GO_VERSION=1.26.0
ARG PY_VERSION=3.13

RUN export DEBIAN_FRONTEND=noninteractive &&\
    apt-get update -yyqq &&\
    apt-get install -yyqq wget tar software-properties-common gcc pkgconf &&\
    add-apt-repository -y ppa:deadsnakes/ppa &&\
    apt-get update -yyqq &&\
    apt-get install -yyqq python${PY_VERSION}-dev python${PY_VERSION}-venv &&\
    ARCH_DIR=$(ls -d /usr/lib/*-linux-gnu | head -n1) &&\
    mv ${ARCH_DIR}/pkgconfig/python-${PY_VERSION}-embed.pc ${ARCH_DIR}/pkgconfig/python3-embed.pc &&\
    ln -s /usr/bin/python${PY_VERSION} /usr/bin/python &&\
    wget https://bootstrap.pypa.io/get-pip.py &&\
    python get-pip.py &&\
    apt-get clean &&\
    rm -rf /var/lib/apt/lists/* get-pip.py &&\
    ARCH=$(dpkg --print-architecture) &&\
    wget https://dl.google.com/go/go${GO_VERSION}.linux-${ARCH}.tar.gz && \
    tar -C /usr/local -xzf go*.linux-${ARCH}.tar.gz && \
    rm go*.linux-${ARCH}.tar.gz

COPY . /build

ENV PATH=$PATH:/usr/local/go/bin

RUN go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest &&\
    cd /usr/local/bin &&\
    CGO_ENABLED=1 /root/go/bin/xcaddy build --with github.com/mliezun/caddy-snake=/build &&\
    rm -rf /build
