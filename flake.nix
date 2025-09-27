{
  inputs = {
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }: 
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        packages = {
          default = pkgs.buildGoModule {
            pname = "uds-proxy";
            version = "0.1.0";
            src = ./.;
            vendorHash = "sha256-raKARqHxii5Qmi+khIjacz7ZPzhNF6nkOl2/UriNYaU=";
            buildInputs = [];
          };
        };
      });
}
