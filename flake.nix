{
  description = "GoBard native development environment";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = import nixpkgs { inherit system; };
      libdave = pkgs.stdenv.mkDerivation {
        pname = "libdave";
        version = "1.2.1";
        src = pkgs.fetchurl {
          url = "https://github.com/discord/libdave/releases/download/v1.2.1/cpp/libdave-Linux-X64-boringssl.zip";
          hash = "sha256-f98Qp9+JRAYpFGMTCjIsBNfeWAhbYCUAhmoRh06JB7Q=";
        };
        nativeBuildInputs = with pkgs; [ autoPatchelfHook unzip ];
        buildInputs = [ pkgs.stdenv.cc.cc.lib ];
        dontUnpack = true;
        installPhase = ''
          runHook preInstall
          install -d "$out/include" "$out/lib/pkgconfig"
          unzip -p "$src" include/dave/dave.h > "$out/include/dave.h"
          unzip -p "$src" lib/libdave.so > "$out/lib/libdave.so"
          chmod 0555 "$out/lib/libdave.so"
          substitute ${./nix/dave.pc} "$out/lib/pkgconfig/dave.pc" --subst-var out
          runHook postInstall
        '';
      };
    in
    {
      packages.${system}.libdave = libdave;
      checks.${system}.libdave = libdave;

      devShells.${system}.default = pkgs.mkShell {
        packages = with pkgs; [
          deno
          ffmpeg
          gcc
          go_1_27
          libogg
          libopus
          libsodium
          libdave
          opusfile
          pkg-config
          python3
          unzip
          yt-dlp
        ];
        env = {
          CGO_ENABLED = "1";
          DENO_NO_UPDATE_CHECK = "1";
          GOTOOLCHAIN = "local";
          PKG_CONFIG_PATH = "${libdave}/lib/pkgconfig";
        };
        shellHook = ''
          go version
        '';
      };
    };
}
