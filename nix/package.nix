{
  lib,
  buildGoModule,
  version ? "dev",
}:
buildGoModule {
  pname = "baseten";
  inherit version;

  src = lib.cleanSource ../.;

  # Update by setting to lib.fakeHash and copying the hash from the build error.
  vendorHash = "sha256-ghtyvdt80fy6JCw0J20a70fai9fUQyJW5vS8gQibZWE=";

  subPackages = [ "cmd/baseten" ];

  env.CGO_ENABLED = 0;

  ldflags = [
    "-s"
    "-w"
    "-X github.com/basetenlabs/baseten-cli/internal/cmd.Version=${version}"
  ];

  meta = {
    description = "CLI for Baseten";
    homepage = "https://github.com/basetenlabs/baseten-cli";
    license = lib.licenses.mit;
    mainProgram = "baseten";
  };
}
