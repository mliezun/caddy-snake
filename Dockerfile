ARG PY_VERSION=3.13
FROM python:${PY_VERSION}

ARG GO_VERSION=1.25.0

RUN apt-get update -yyqq && \
    apt-get install -yyqq wget tar gcc pkgconf && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

RUN ARCH=$(dpkg --print-architecture) && \
    wget https://dl.google.com/go/go${GO_VERSION}.linux-${ARCH}.tar.gz && \
    tar -C /usr/local -xzf go*.linux-${ARCH}.tar.gz && \
    rm go*.linux-${ARCH}.tar.gz

ENV PATH=$PATH:/usr/local/go/bin

# Setup pkg-config for python3-embed
RUN PC_FILE=$(find /usr/local/lib/pkgconfig -name "python-${PY_VERSION}-embed.pc" | head -n 1) && \
    if [ -z "$PC_FILE" ]; then echo "Error: python-${PY_VERSION} embed pc file not found"; exit 1; fi && \
    ln -sf "$PC_FILE" /usr/local/lib/pkgconfig/python3-embed.pc

COPY . /build

RUN go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest && \
    cd /usr/local/bin && \
    CGO_ENABLED=1 /root/go/bin/xcaddy build --with github.com/mliezun/caddy-snake=/build && \
    rm -rf /build
