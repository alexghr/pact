{ pkgs, lib, ... }:
{
  scripts = {
    p-fmt = {
      packages = [
        pkgs.git
        pkgs.findutils
        pkgs.nixfmt
        pkgs.go_1_27
      ];
      exec = builtins.readFile ./fmt.sh;
    };

    p-git-client.exec = ''
      set -euo pipefail

      for arch in amd64 arm64; do
        CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath \
          -o "internal/images/context/agent/bin/pact-git-client-linux-$arch" ./cmd/git-client
      done
    '';

    p-agent.exec = ''
      set -euo pipefail

      for arch in amd64 arm64; do
        CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath \
          -o "internal/images/context/agent/bin/pact-agent-linux-$arch" ./cmd/pact-agent
      done
    '';

    p-harness-build.exec = ''
      set -euo pipefail

      p-git-client
      p-agent
    '';

    # The launcher embeds the prepared container inputs, including its helpers.
    p-host-build.exec = ''
      set -euo pipefail

      go build "$@" ./cmd/pact
      ${lib.optionalString pkgs.stdenv.hostPlatform.isDarwin ''
        go build "$@" ./cmd/pact-approvals
      ''}
    '';

    p-build = {
      packages = [ pkgs.git ];
      exec = ''
        set -euo pipefail

        version="$(git rev-parse --abbrev-ref HEAD)-$(git rev-parse --short HEAD)"
        p-harness-build
        p-host-build -ldflags "-X=github.com/alexghr/pact/internal/version.Version=$version"
      '';
    };

    p-test = {
      packages = [
        pkgs.bash
        pkgs.coreutils
        pkgs.curl
        pkgs.git
        pkgs.openssh
        pkgs.socat
      ];
      exec = ''
        go test ./...
      '';
    };

    p-lint.exec = ''
      set -euo pipefail

      p-fmt --check
      go vet ./...
    '';

    p-deps = {
      packages = [
        pkgs.git
        pkgs.jq
        pkgs.coreutils
      ];
      exec = builtins.readFile ./review/deps.sh;
    };

    p-layout = {
      packages = [ pkgs.git ];
      exec = builtins.readFile ./review/layout.sh;
    };

    p-config-naming = {
      packages = [ pkgs.git ];
      exec = builtins.readFile ./review/config-naming.sh;
    };

    p-docker-boundary = {
      packages = [ pkgs.git ];
      exec = builtins.readFile ./review/docker-boundary.sh;
    };

    p-ci = {
      packages = [
        pkgs.jq
        pkgs.coreutils
      ];
      exec = builtins.readFile ./ci.sh;
    };

    p-ci-report = {
      packages = [ pkgs.jq ];
      exec = ''
        exec jq -er -f ${pkgs.writeText "ci-report.jq" (builtins.readFile ./ci-report.jq)} "$@"
      '';
    };
  };
}
