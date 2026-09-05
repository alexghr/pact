FROM ubuntu:26.04

ARG CODEX_VERSION=latest
ARG TARGETARCH

ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      git \
      gosu \
      libatomic1 \
      ripgrep && \
    rm -rf /var/lib/apt/lists/*
 
ENV NODE_VERSION=26.8.1

RUN case "$TARGETARCH" in \
    amd64) NODE_ARCH=x64; NODE_SHASUM=b2b76660fa4ded4e0b2a41ee3c0c651cd52ea8170ead91ebac1e147ac3d55643 ;; \
    arm64) NODE_ARCH=arm64; NODE_SHASUM=d5f973ce975e4bd03e6c2038260f7e9201615aa8e1ee293c72f8dcc2a6d9fddb ;; \
    *) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
  esac && \
  NODE_ARCHIVE="node-v${NODE_VERSION}-linux-${NODE_ARCH}.tar.gz" && \
  curl -fLSs --retry 5 --retry-delay 1 -o "/tmp/$NODE_ARCHIVE" "https://nodejs.org/dist/v${NODE_VERSION}/$NODE_ARCHIVE" && \
  printf '%s  %s\n' "$NODE_SHASUM" "/tmp/$NODE_ARCHIVE" | sha256sum --check && \
  tar -xf "/tmp/$NODE_ARCHIVE" -C /usr/local --strip-components=1 && \
  rm -f "/tmp/$NODE_ARCHIVE" && \
  npm install --global corepack "@openai/codex@${CODEX_VERSION}" && \
  corepack enable

RUN groupadd pact && useradd --gid pact --shell /bin/bash --create-home pact && \
  mkdir -p /home/pact/workspace && chown pact:pact /home/pact/workspace && \
  mkdir /opt/pact

COPY ./entrypoint.sh /opt/pact/entrypoint.sh
COPY ./artifacts-proxy.mjs /opt/pact/artifacts-proxy.mjs
RUN chmod 0755 /opt/pact/entrypoint.sh

ENV CODEX_HOME=/home/pact/.codex
ENV CI=0

WORKDIR /home/pact/workspace
ENTRYPOINT ["/opt/pact/entrypoint.sh"]
