FROM ubuntu:22.04

ARG GO_VERSION=1.26.0
ARG PY_VERSION=3.13

# Persist apt retries across steps (helps flaky mirrors / intermittent 5xx during CI multi-arch builds).
RUN printf 'Acquire::Retries "12";\n' >/etc/apt/apt.conf.d/80-retries

# add-apt-repository talks to Launchpad; intermittent HTTP 504s break docker-publish during CI builds.
RUN /bin/bash -eux <<SETUP
	export DEBIAN_FRONTEND=noninteractive
	apt-get update -yyqq
	apt-get install -yyqq wget tar software-properties-common ca-certificates
	retry=1
	max=12
	delay=15
	until add-apt-repository -y ppa:deadsnakes/ppa; do
		retry=$((retry+1))
		if [[ "${retry}" -gt "${max}" ]]; then exit 1; fi
		sleep "${delay}"
	done
	apt-get update -yyqq
	apt-get install -yyqq "python${PY_VERSION}-venv"
	ln -sf "/usr/bin/python${PY_VERSION}" /usr/bin/python
	wget -q https://bootstrap.pypa.io/get-pip.py
	python get-pip.py
	apt-get clean
	rm -rf /var/lib/apt/lists/* get-pip.py
	ARCH="$(dpkg --print-architecture)"
	wget -q "https://dl.google.com/go/go${GO_VERSION}.linux-${ARCH}.tar.gz"
	tar -C /usr/local -xzf "go${GO_VERSION}.linux-${ARCH}.tar.gz"
	rm "go${GO_VERSION}.linux-${ARCH}.tar.gz"
SETUP

COPY . /build

ENV PATH=$PATH:/usr/local/go/bin

RUN go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest &&\
    cd /usr/local/bin &&\
    CGO_ENABLED=0 /root/go/bin/xcaddy build --with github.com/mliezun/caddy-snake=/build &&\
    rm -rf /build
