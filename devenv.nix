{
  pkgs,
  lib,
  config,
  inputs,
  ...
}:
{
  profiles = {
    # load development packages just for development in order to make the closure smaller in CI
    dev.module =
      let
        agents = import inputs.agents { inherit pkgs; };
      in
      { pkgs, ... }:
      {
        languages.go.enable = true;

        packages = [
          pkgs.git
          agents.codex
        ];

        git-hooks.hooks = {
          format = {
            enable = true;
            name = "Check formatting";
            entry = "${config.scripts.p-fmt.scriptPackage}/bin/p-fmt --check";
            files = "\\.(nix|go)$";
            pass_filenames = false;
          };
        };
      };

    ci.module = { pkgs, ... }: {
      # languages.go.enable brings in a bunch of tools which don't matter in CI (e.g. gopls)
      packages = [
        pkgs.go
      ];
      env.GOTOOLCHAIN = "local";
    };
  };

  scripts = {
    p-fmt = {
      packages = [
        pkgs.git
        pkgs.findutils
        pkgs.nixfmt
        pkgs.go
      ];
      exec = ''
        set -euo pipefail

        if [[ $# -eq 0 ]]; then
          git ls-files -z '*.nix' | xargs -0 -r nixfmt
          git ls-files -z '*.go' | xargs -0 -r gofmt -w
          exit 0
        fi
        if [[ $# -ne 1 || "$1" != --check ]]; then
          printf 'Usage: p-fmt [--check]\n' >&2
          exit 2
        fi

        git ls-files -z '*.nix' | xargs -0 -r nixfmt --check
        unformatted=$(git ls-files -z '*.go' | xargs -0 -r gofmt -l)
        if [[ -n "$unformatted" ]]; then
          printf 'Go files need formatting:\n%s\n' "$unformatted" >&2
          exit 1
        fi
      '';
    };

    p-build.exec = ''
      go build ./cmd/pact
    '';

    p-test.exec = ''
      go test ./...
    '';

    p-lint.exec = ''
      go vet ./...
    '';

    p-ci.exec = ''
      set -euo pipefail

      p-fmt --check
      p-lint
      p-test
      p-build
    '';

  };
}
