{
  description = "Pact's container tools";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    agents = {
      url = "github:alexghr/agents.nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
    graphene = {
      url = "github:alexghr/graphene";
      flake = false;
    };
  };

  outputs =
    {
      nixpkgs,
      agents,
      graphene,
      ...
    }:
    {
      packages = nixpkgs.lib.genAttrs [ "x86_64-linux" "aarch64-linux" ] (
        system:
        let
          pkgs = import nixpkgs { inherit system; };
          agentPackages = agents.packages.${system};
          graphenePackage = pkgs.callPackage "${graphene}/package.nix" { };
          # Add or remove bundled skills here, then rebuild Pact and its image.
          skills = pkgs.linkFarm "pact-skills" [
            {
              name = "share/pact/skills/graphene-stacked-prs";
              path = "${graphene}/skills/graphene-stacked-prs";
            }
          ];
          tools = pkgs.buildEnv {
            name = "pact-tools";
            paths = with pkgs; [
              nix
              devenv
              agentPackages.codex
              graphenePackage
              skills
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
