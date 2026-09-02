{ pkgs, lib, config, inputs, ... }:
let
  pkgs-unstable = import inputs.nixpkgs-unstable { system = pkgs.stdenv.system; };
in
  {
    packages = with pkgs;
      [
        git
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
  }
