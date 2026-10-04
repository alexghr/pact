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
    let
      pkgs = import nixpkgs { system = "x86_64-linux"; };
      agentPackages = agents.packages.x86_64-linux;
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
          which
          xz
        ];
      };
    in
    {
      packages.x86_64-linux = {
        pact-tools = tools;
        default = tools;
      };
    };
}
