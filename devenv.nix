{ pkgs, lib, config, inputs, ... }:
let
  pkgs-unstable = import inputs.nixpkgs-unstable { system = pkgs.stdenv.system; };
  agents = import inputs.agents { inherit pkgs; };
in
  {
    packages = with pkgs;
      [
        git
        sqlite
        pkgs-unstable.gh
        agents.codex
      ];

    languages.go = {
      enable = true;
      package = pkgs-unstable.go;
      lsp = {
        enable = true;
        package = pkgs-unstable.gopls;
      };
    };

    scripts = {
      pact_dev = {
        exec = ''
          watchexec -e go -r -n -- go run \
            "-ldflags=-X github.com/alexghr/pact/internal/web.Debug=true" \
            ./cmd/pact web
        '';
        packages = [pkgs.watchexec];
      };

      pact_build.exec = ''
        go build ./cmd/pact
      '';

      pact_test.exec = ''
        go test ./...
      '';

      pact_lint.exec = ''
        go vet ./...
      '';

      pact_ci.exec = ''
        set -euo pipefail

        pact_lint
        pact_test
        pact_build
      '';
    };
  }
