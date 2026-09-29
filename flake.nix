{
  description = "CLI for Baseten";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-26.05";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
        baseten = pkgs.callPackage ./nix/package.nix {
          version = if self ? shortRev then "unstable-${self.shortRev}" else "dev";
        };
      in
      {
        packages = {
          default = baseten;
          inherit baseten;
        };

        checks = {
          build = baseten;
          # Smoke test: the built binary runs and reports the expected version.
          version = pkgs.runCommand "baseten-version-check" { } ''
            got="$(${pkgs.lib.getExe baseten} version)"
            want=${pkgs.lib.escapeShellArg baseten.version}
            if [ "$got" != "$want" ]; then
              echo "baseten version reported '$got', expected '$want'" >&2
              exit 1
            fi
            ${pkgs.lib.getExe baseten} --help > /dev/null
            touch $out
          '';
        };
      }
    )
    // {
      overlays.default = final: prev: {
        baseten = final.callPackage ./nix/package.nix { };
      };
    };
}
