# Noble ships Python 3.12 in main; newer versions require deadsnakes (HTTP apt only — no Launchpad HTTPS API).
FROM ubuntu:24.04

ARG GO_VERSION=1.26.0
ARG PY_VERSION=3.13

ENV GO_VERSION=${GO_VERSION} \
    PY_VERSION=${PY_VERSION}

RUN printf 'Acquire::Retries "12";\n' >/etc/apt/apt.conf.d/80-retries

RUN /bin/bash -eux <<'SETUP'
	export DEBIAN_FRONTEND=noninteractive
	UBUNTU_CODENAME=noble
	export UBUNTU_CODENAME
	apt-get update -yyqq
	apt-get install -yyqq wget tar ca-certificates gnupg
	if [[ "${PY_VERSION}" == "3.12" ]]; then
		apt-get install -yyqq python3.12-venv
		VENV_PYTHON=python3.12
	else
		install -d -m 0755 /usr/share/keyrings
		keyring=/usr/share/keyrings/deadsnakes.gpg
		n=0
		until gpg --batch --no-default-keyring --keyring "${keyring}" \
			--keyserver hkps://keyserver.ubuntu.com:443 \
			--recv-keys F23C5A6CF475977595C89F51BA6932366A755776; do
			n=$((n + 1))
			if [[ "${n}" -ge 12 ]]; then exit 1; fi
			sleep 10
		done
		echo "deb [signed-by=${keyring}] https://ppa.launchpadcontent.net/deadsnakes/ppa/ubuntu ${UBUNTU_CODENAME} main" \
			>/etc/apt/sources.list.d/deadsnakes-ppa.list
		retry=0
		until apt-get update -yyqq; do
			retry=$((retry + 1))
			if [[ "${retry}" -ge 18 ]]; then exit 1; fi
			sleep 10
		done
		apt-get install -yyqq "python${PY_VERSION}-venv"
		VENV_PYTHON="python${PY_VERSION}"
	fi
	ln -sf "/usr/bin/${VENV_PYTHON}" /usr/bin/python
	wget -q https://bootstrap.pypa.io/get-pip.py
	# PEP 668: Ubuntu marks distro Python as "externally managed"; Docker image intentionally uses pip on that interpreter for xcaddy build helpers.
	python get-pip.py --break-system-packages
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
