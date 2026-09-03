{ pkgs, lib, config, inputs, ... }:
let
  pkgs-unstable = import inputs.nixpkgs-unstable { system = pkgs.stdenv.system; };
in
  {
    packages = with pkgs;
      [
        git
        sqlite
        pkgs-unstable.codex
        pkgs-unstable.gh
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
