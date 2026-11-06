{
  pkgs,
  config,
  inputs,
  ...
}:
{
  imports = [ ./scripts ];

  # go-sqlite3 compiles its bundled SQLite into the host executable through CGO.
  packages = [ pkgs.stdenv.cc ];
  env.CGO_ENABLED = "1";

  profiles = {
    # load development packages just for development in order to make the closure smaller in CI
    dev.module =
      let
        agents = import inputs.agents { inherit pkgs; };
      in
      { pkgs, ... }:
      {
        packages = [
          pkgs.go_1_27
          pkgs.gopls
          pkgs.docker-client
          pkgs.git
          agents.codex
        ];
        env.GOTOOLCHAIN = "local";

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
      packages = [
        pkgs.go_1_27
      ];
      env.GOTOOLCHAIN = "local";
    };
  };
}
