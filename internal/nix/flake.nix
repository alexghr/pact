{
  description = "Pact's container tools";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    agents = {
      url = "github:alexghr/agents.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    { nixpkgs, agents, ... }:
    {
      packages = nixpkgs.lib.genAttrs [ "x86_64-linux" "aarch64-linux" ] (
        system:
        let
          pkgs = import nixpkgs { inherit system; };
          agentPackages = agents.packages.${system};
          tools = pkgs.buildEnv {
            name = "pact-tools";
            paths = with pkgs; [
              nix
              devenv
              agentPackages.codex
              bashInteractive
              coreutils
              curl
              diffutils
              docker-client
              findutils
              gawk
              git
              gnugrep
              gnused
              gnutar
              gzip
              less
              openssh
              procps
              ripgrep
              socat
              which
              xz
            ];
          };
        in
        {
          pact-tools = tools;
          default = tools;
        }
      );
    };
}
