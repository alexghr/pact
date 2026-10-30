FROM nixos/nix@sha256:bf1d938835ab96312f098fa6c2e9cab367728e0aad0646ee3e02a787c80d8fb8

COPY nix.conf /etc/nix/nix.conf
COPY entrypoint.sh /opt/pact/nix-daemon.sh
ENTRYPOINT ["bash", "/opt/pact/nix-daemon.sh"]
