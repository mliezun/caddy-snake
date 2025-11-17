FROM ubuntu:22.04

ARG GO_VERSION=1.25.0
ARG PY_VERSION=3.13

COPY . /build
COPY scripts/setup-python-and-build.sh /usr/local/bin/setup-python-and-build.sh
RUN chmod +x /usr/local/bin/setup-python-and-build.sh

RUN bash /usr/local/bin/setup-python-and-build.sh \
    --python-version ${PY_VERSION} \
    --go-version ${GO_VERSION} \
    --build-caddy \
    --caddy-snake-path /build \
    --output-caddy-path /usr/local/bin/caddy &&\
    ln -s /usr/bin/python${PY_VERSION} /usr/bin/python &&\
    wget https://bootstrap.pypa.io/get-pip.py &&\
    python get-pip.py &&\
    apt-get clean &&\
    rm -rf /var/lib/apt/lists/* get-pip.py &&\
    rm -rf /build
