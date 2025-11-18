FROM ubuntu:22.04

ARG GO_VERSION=1.25.0
ARG PY_VERSION=3.14

RUN export DEBIAN_FRONTEND=noninteractive &&\
    apt-get update -yyqq &&\
    apt-get install -yyqq wget tar software-properties-common gcc pkgconf &&\
    add-apt-repository -y ppa:deadsnakes/ppa &&\
    apt-get update -yyqq &&\
    apt-get install -yyqq python${PY_VERSION}-dev &&\
    ARCH_DIR=$(ls -d /usr/lib/*-linux-gnu | head -n1) &&\
    mv ${ARCH_DIR}/pkgconfig/python-${PY_VERSION}-embed.pc ${ARCH_DIR}/pkgconfig/python3-embed.pc &&\
    rm -rf /var/lib/apt/lists/* &&\
    wget https://dl.google.com/go/go${GO_VERSION}.linux-amd64.tar.gz && \
    tar -C /usr/local -xzf go*.linux-amd64.tar.gz && \
    rm go*.linux-amd64.tar.gz

ENV PATH=/usr/local/go/bin:$PATH

RUN go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest &&\
    cd /usr/local/bin &&\
    CGO_ENABLED=1 /root/go/bin/xcaddy build --with github.com/mliezun/caddy-snake &&\
    rm -rf /build

CMD ["cp", "/usr/local/bin/caddy", "/output/caddy"]
