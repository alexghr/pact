# Build from the repository root:
#   ./shell p-harness-build
#   docker build --tag pact-codex internal/nix/context
FROM ubuntu:26.04@sha256:da6fc2be547864451aa253836dd926da33623312df4a9a243e35dc877c378a78

ARG TARGETARCH

RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates gosu && \
    rm -rf /var/lib/apt/lists/* && \
    groupadd pact && useradd --gid pact --shell /bin/bash --create-home pact && \
    mkdir -p /home/pact/workspace /opt/pact /etc/nix && \
    chown pact:pact /home/pact/workspace

# Stable paths let running agents see tool and skill updates through the store.
RUN mkdir -p /etc/codex && \
    ln -s /nix/var/nix/profiles/pact-tools /opt/pact/tools && \
    ln -s /nix/var/nix/pact-skills /etc/codex/skills
# Both commands use cmd/git-client; argv[0] selects transport or signing.
COPY --chmod=0755 bin/pact-git-client-linux-${TARGETARCH} /usr/local/bin/pact-git-sign
RUN ln -s pact-git-sign /usr/local/bin/pact-git-ssh
COPY --chmod=0755 bin/pact-agent-linux-${TARGETARCH} /usr/local/bin/pact-agent
COPY nix.conf /etc/nix/nix.conf
COPY entrypoint.sh /opt/pact/entrypoint.sh
COPY git-identity.sh /opt/pact/git-identity.sh
COPY host-relay.sh /opt/pact/host-relay.sh
COPY agent-instructions.md /opt/pact/agent-instructions.md
RUN chmod 0755 /opt/pact/entrypoint.sh

ENV CODEX_HOME=/home/pact/.codex
ENV NIX_REMOTE=daemon
ENV CI=0

WORKDIR /home/pact/workspace
ENTRYPOINT ["/opt/pact/entrypoint.sh"]
