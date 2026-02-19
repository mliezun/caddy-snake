FROM ubuntu:22.04

ARG GO_VERSION=1.26.0

RUN export DEBIAN_FRONTEND=noninteractive &&\
    apt-get update -yyqq &&\
    apt-get install -yyqq wget tar ca-certificates &&\
    rm -rf /var/lib/apt/lists/* &&\
    ARCH=$(dpkg --print-architecture) &&\
    wget https://dl.google.com/go/go${GO_VERSION}.linux-${ARCH}.tar.gz && \
    tar -C /usr/local -xzf go*.linux-${ARCH}.tar.gz && \
    rm go*.linux-${ARCH}.tar.gz

ENV PATH=/usr/local/go/bin:$PATH

RUN go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest &&\
    cd /usr/local/bin &&\
    CGO_ENABLED=0 /root/go/bin/xcaddy build --with github.com/mliezun/caddy-snake &&\
    rm -rf /build

CMD ["cp", "/usr/local/bin/caddy", "/output/caddy"]
