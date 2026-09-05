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

ENV GO_VERSION=1.27.1

RUN case "$TARGETARCH" in \
    amd64) GO_SHASUM=63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445 ;; \
    arm64) GO_SHASUM=3450b45a3f9ee8568792736a5c5e70a1f2e9b36c35a8f74958c03e51d7d92bec ;; \
    *) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
  esac && \
  GO_ARCHIVE="go${GO_VERSION}.linux-${TARGETARCH}.tar.gz" && \
  curl -fLSs --retry 5 --retry-delay 1 -o "/tmp/$GO_ARCHIVE" "https://go.dev/dl/$GO_ARCHIVE" && \
  printf '%s  %s\n' "$GO_SHASUM" "/tmp/$GO_ARCHIVE" | sha256sum --check && \
  mkdir /opt/go && tar -xf "/tmp/$GO_ARCHIVE" -C /opt/go --strip-components=1 && \
  rm -f "/tmp/$GO_ARCHIVE"

ENV PATH="/opt/go/bin:$PATH"
 
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
