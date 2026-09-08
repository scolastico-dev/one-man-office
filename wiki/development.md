# Development and testing

## Build from source

Requirements are Go (see `go.mod` for the exact version) and `git`. The build
is pure Go with `CGO_ENABLED=0` everywhere, including the SQLite driver.
Linux, macOS, and Windows are supported on amd64 and arm64. Windows uses
ConPTY and a named pipe; Linux and macOS use a PTY and a Unix socket.

```bash
make install                  # -> /usr/local/bin/omo  (may need sudo)
make install PREFIX=~/.local  # -> ~/.local/bin/omo
```

`omo` must be on the `PATH` of the agents themselves because they invoke it by
name to send mail, queue jobs, and report completion.

Other targets:

- `make build` -> `./bin/omo`
- `make test`
- `make check` -> fmt + vet + test
- `make cross` -> Linux/macOS/Windows, amd64 + arm64
- `make cross-linux`, `make cross-darwin`, `make cross-windows` -> one platform
- `make clean`
- `make help`

## Testing

The kernel is fully testable without any AI. A scenario-driven fake agent,
`omo fake-agent`, stands in for the CLI, and the integration tests spawn it in
real PTYs/ConPTYs to exercise:

- spawn -> queue -> review -> merge
- smoke alarm -> firefighter
- restart recovery

```bash
make test
```

`omo fake-agent [--scenario <file>] [--auto-role <role>]` is a hidden internal
command used by `--mock` and the integration suite. It is not part of the
normal user or agent workflow.

Real CLI handshakes are opt-in because they make a model request. Each command
disables spawn retries and tests only `omo ready`:

```bash
OMO_LIVE_AGENT_CLI=codex go test ./internal/supervisor -run TestLiveAgentCLIHandshake -count=1
OMO_LIVE_AGENT_CLI=gemini go test ./internal/supervisor -run TestLiveAgentCLIHandshake -count=1
```

## Contributing

[CLAUDE.md](../CLAUDE.md) is the technical orientation for anyone changing the
repository: architecture, package map, invariants, and the change checklist.
`make check` is the required local gate and matches CI.
