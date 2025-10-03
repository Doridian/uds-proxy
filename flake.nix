{
  inputs = {
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }: 
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        package = pkgs.buildGoModule {
          pname = "uds-proxy";
          version = "0.1.0";
          src = ./.;
          vendorHash = "sha256-TB+vWr8ortGSZeggf/hnHpfaJxmI29W6swkGywZLeWs=";
          buildInputs = [];
        };
      in
      {
        packages = {
          default = package;
          uds-proxy = package;
        };
      });
}
