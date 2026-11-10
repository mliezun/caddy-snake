# The builder runs on the build host's native platform and cross-compiles Caddy
# for the target (CGO_ENABLED=0, so GOARCH is all it takes). This keeps Go
# compilation out of QEMU when building arm64/riscv64 images on amd64 runners.
FROM --platform=$BUILDPLATFORM ubuntu:22.04 AS builder

ARG GO_VERSION=1.26.10
ARG TARGETOS
ARG TARGETARCH

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

RUN go install github.com/caddyserver/xcaddy/cmd/xcaddy@v0.4.6 &&\
    cd /usr/local/bin &&\
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    /root/go/bin/xcaddy build --with github.com/mliezun/caddy-snake=/build --with github.com/mliezun/caddy-snake/apps

FROM ubuntu:22.04

ARG PY_VERSION=3.13
ARG TARGETARCH
# deadsnakes does not publish interpreters for riscv64, so riscv64 images use
# python-build-standalone instead. Bump together with the release tag used by
# cmd/embed when refreshing Python patch versions.
ARG PBS_RELEASE=20261026

RUN export DEBIAN_FRONTEND=noninteractive &&\
    apt-get update -yyqq &&\
    apt-get upgrade -yyqq &&\
    if [ "${TARGETARCH}" = "riscv64" ]; then \
        apt-get install -yyqq wget ca-certificates &&\
        PBS_BASE="https://github.com/astral-sh/python-build-standalone/releases/download/${PBS_RELEASE}" &&\
        wget -q "${PBS_BASE}/SHA256SUMS" &&\
        PBS_FILE=$(grep -oE "cpython-${PY_VERSION}\.[0-9]+\+${PBS_RELEASE}-riscv64-unknown-linux-gnu-install_only_stripped\.tar\.gz" SHA256SUMS | head -n1) &&\
        if [ -z "${PBS_FILE}" ]; then echo "no python-build-standalone ${PY_VERSION} riscv64 build in ${PBS_RELEASE}" >&2; exit 1; fi &&\
        wget -q "${PBS_BASE}/${PBS_FILE}" &&\
        grep " ${PBS_FILE}\$" SHA256SUMS | sha256sum -c - &&\
        tar -C /opt -xzf "${PBS_FILE}" &&\
        rm -f SHA256SUMS "${PBS_FILE}" &&\
        for bin in "python${PY_VERSION}" python3 python; do ln -sf /opt/python/bin/python${PY_VERSION} /usr/bin/$bin; done &&\
        ln -sf /opt/python/bin/pip${PY_VERSION} /usr/local/bin/pip &&\
        ln -sf /opt/python/bin/pip${PY_VERSION} /usr/local/bin/pip3 &&\
        rm -f /opt/python/lib/python*/site-packages/pip/_vendor/bom.cdx.json; \
    else \
        apt-get install -yyqq wget software-properties-common &&\
        add-apt-repository -y ppa:deadsnakes/ppa &&\
        apt-get update -yyqq &&\
        apt-get install -yyqq python${PY_VERSION}-venv &&\
        # Point both python and python3 at the image Python. Ubuntu ships python3 as
        # the system interpreter (e.g. 3.10 on 22.04); workers default to "python3",
        # so leaving the system symlink breaks pip-installed packages (see #219).
        ln -sf /usr/bin/python${PY_VERSION} /usr/bin/python &&\
        ln -sf /usr/bin/python${PY_VERSION} /usr/bin/python3 &&\
        wget -q https://bootstrap.pypa.io/get-pip.py &&\
        # Containers intentionally install pip into the image Python; PEP 668 marks
        # Debian/Ubuntu interpreters as externally managed on newer releases.
        python get-pip.py --break-system-packages &&\
        rm -f get-pip.py &&\
        # pip>=26.2 ships a vendored CycloneDX SBOM (bom.cdx.json). Trivy treats that
        # third-party SBOM as installed packages and fails on pip's vendored copies of
        # msgpack/setuptools even though they are not importable top-level deps.
        rm -f /usr/local/lib/python*/dist-packages/pip/_vendor/bom.cdx.json; \
    fi &&\
    apt-get clean &&\
    rm -rf /var/lib/apt/lists/*

RUN groupadd -r caddy && useradd -r -g caddy -d /home/caddy -s /usr/sbin/nologin caddy && \
    mkdir -p /home/caddy && chown caddy:caddy /home/caddy

COPY --from=builder /usr/local/bin/caddy /usr/local/bin/caddy

USER caddy
