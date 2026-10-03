# Build from the repository root:
#   docker build --tag pact-codex --file internal/nix/Dockerfile internal/nix
FROM nixos/nix@sha256:bf1d938835ab96312f098fa6c2e9cab367728e0aad0646ee3e02a787c80d8fb8 AS tools

COPY flake.nix flake.lock /tmp/pact-tools/
RUN nix --extra-experimental-features 'nix-command flakes' build \
    /tmp/pact-tools#pact-tools --no-update-lock-file --out-link /opt/pact/tools
RUN export PATH="/opt/pact/tools/bin:$PATH" && \
    nix-store --query --requisites /opt/pact/tools > /tmp/closure && \
    nix-store --dump-db $(cat /tmp/closure) > /opt/pact/store-registration && \
    sed 's|^/nix/store/||' /tmp/closure > /tmp/store-paths && \
    tar -C /nix/store -cf /opt/pact/store.tar -T /tmp/store-paths

FROM ubuntu:26.04@sha256:da6fc2be547864451aa253836dd926da33623312df4a9a243e35dc877c378a78

RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates gosu && \
    rm -rf /var/lib/apt/lists/* && \
    groupadd pact && useradd --gid pact --shell /bin/bash --create-home pact && \
    mkdir -p /home/pact/workspace /opt/pact /etc/nix && \
    chown pact:pact /home/pact/workspace

COPY --from=tools /opt/pact/ /opt/pact/
COPY nix.conf /etc/nix/nix.conf
COPY entrypoint.sh /opt/pact/entrypoint.sh
COPY agent-instructions.md /opt/pact/agent-instructions.md
RUN chmod 0755 /opt/pact/entrypoint.sh

ENV CODEX_HOME=/home/pact/.codex
ENV NIX_REMOTE=local
ENV CI=0

WORKDIR /home/pact/workspace
ENTRYPOINT ["/opt/pact/entrypoint.sh"]
