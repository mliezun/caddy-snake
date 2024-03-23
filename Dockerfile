FROM ubuntu:latest

ARG GO_VERSION=1.22.1
ARG PY_VERSION=3.12

RUN export DEBIAN_FRONTEND=noninteractive &&\
    apt-get update -yyqq &&\
    apt-get install -yyqq wget tar software-properties-common gcc pkgconf &&\
    add-apt-repository -y ppa:deadsnakes/ppa &&\
    apt-get update -yyqq &&\
    apt-get install -yyqq python${PY_VERSION}-dev &&\
    mv /usr/lib/x86_64-linux-gnu/pkgconfig/python-${PY_VERSION}-embed.pc /usr/lib/x86_64-linux-gnu/pkgconfig/python3-embed.pc &&\
    rm -rf /var/lib/apt/lists/* &&\
    wget https://dl.google.com/go/go${GO_VERSION}.linux-amd64.tar.gz && \
    tar -C /usr/local -xzf go*.linux-amd64.tar.gz && \
    rm go*.linux-amd64.tar.gz

COPY . /build

ENV PATH=$PATH:/usr/local/go/bin

RUN go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest &&\
    cd /usr/local/bin &&\
    CGO_ENABLED=1 /root/go/bin/xcaddy build --with github.com/mliezun/caddy-snake=/build &&\
    rm -rf /build
