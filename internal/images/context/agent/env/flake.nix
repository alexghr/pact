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
            {
              name = "share/pact/skills/review-pr";
              path = ./skills/review-pr;
            }
            {
              name = "share/pact/skills/review-design";
              path = ./skills/review-design;
            }
            {
              name = "share/pact/skills/review-tests";
              path = ./skills/review-tests;
            }
            {
              name = "share/pact/skills/review-docs";
              path = ./skills/review-docs;
            }
          ];

          # agent entrypoint
          start = pkgs.writeShellScriptBin "pact-start" ''
            exec ${pkgs.bash}/bin/bash ${./start.sh} \
              ${skills}/share/pact/skills ${./host-relay.sh} ${./git-identity.sh} "$@"
          '';

          tools = pkgs.buildEnv {
            name = "pact-tools";
            paths = with pkgs; [
              start
              nix
              devenv
              agentPackages.pi
              graphenePackage
              skills
              bashInteractive
              coreutils
              curl
              diffutils
              docker-client
              fd
              file
              findutils
              gawk
              git
              gnugrep
              gnused
              gnutar
              gzip
              jq
              less
              openssh
              procps
              ripgrep
              socat
              unzip
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
