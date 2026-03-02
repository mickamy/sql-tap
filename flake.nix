{
  description = "Real-time SQL traffic viewer — proxy daemon + TUI / Web client";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    { self
    , nixpkgs
    , flake-utils
    }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        version = self.shortRev or self.dirtyShortRev or "dev";
      in
      {
        packages = {
          sql-tap = pkgs.buildGoModule {
            pname = "sql-tap";
            inherit version;
            src = pkgs.lib.cleanSource self;
            subPackages = [ "." "cmd/sql-tapd" ];

            # When go.mod or go.sum changes, update this hash by running:
            #   nix build 2>&1 | grep "got:" | awk '{print $2}'
            # and replacing pkgs.lib.fakeHash below with the output.
            vendorHash = pkgs.lib.fakeHash;

            ldflags = [
              "-s"
              "-w"
              "-X main.version=${version}"
            ];

            meta = with pkgs.lib; {
              description = "Watch SQL traffic in real-time with a TUI";
              longDescription = ''
                sql-tap sits between your application and your database
                (PostgreSQL, MySQL, or TiDB), capturing every query and
                displaying it in an interactive terminal UI. Inspect queries,
                view transactions, and run EXPLAIN — all without changing your
                application code.
              '';
              homepage = "https://github.com/mickamy/sql-tap";
              license = licenses.mit;
              maintainers = [ ];
              mainProgram = "sql-tap";
              platforms = platforms.unix;
            };
          };
          default = self.packages.${system}.sql-tap;
        };
      }
    );
}
