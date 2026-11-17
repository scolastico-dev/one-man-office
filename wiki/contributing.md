# Contributing

omo is a Go server with the board, a Vue app, built into the binary.

- [CLAUDE.md](../CLAUDE.md) is the map of the code: the package layout, the
  invariants, the test rules and where each feature lives. Read it first; it
  is kept up to date with the code.
- The [Makefile](../Makefile) has every build and check (`make help` lists
  them):

```bash
make build           # bin/omo (builds the board first: needs Node.js 24 and npm)
make build NO_WEB=1  # bin/omo without the board, no Node.js needed
make check           # gofmt check, go vet, unused code (with staticcheck installed) and the full Go test suite: what CI runs
make web-check       # type-check and unit-test the board
make e2e             # build omo and run the browser tests (Playwright, Chromium)
make cross           # binaries for Linux, macOS and Windows on amd64 and arm64
make notices         # bin/THIRD_PARTY_NOTICES, the licences of the Go modules and the bundled npm packages
```

To run your build next to an omo you use, give it its own home and port (see
[Several homes](home-folder.md#several-homes)) and put its `bin` folder first
on `PATH`, because agents call `omo ai` by name:

```bash
export OMO_HOME=$(mktemp -d)
./bin/omo config set port 18123
PATH="$PWD/bin:$PATH" ./bin/omo
```

No test calls a real model, forge or usage service: agents in tests are a
scripted fake agent, and every test uses a temporary home.

Pull requests follow [the template](../.github/PULL_REQUEST_TEMPLATE.md) and
conventional commit messages. A change in what users see updates this wiki in
the same pull request. Plugins are listed through a pull request to
`plugins/index.yaml`, described in [plugins/README.md](../plugins/README.md).
