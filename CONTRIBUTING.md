# Contributing

Contributions are welcome! Here's how to get started.

## Setup

```bash
git clone https://github.com/go-rotini/mcp.git
cd mcp
go mod download
make all   # run all project processes
```

## Making Changes

1. Fork the repository and create a branch from `main`.
2. Write tests for any new functionality.
3. Ensure `make all` passes before submitting a pull request.
4. Use [Conventional Commits](https://www.conventionalcommits.org/) for commit messages (e.g., `feat:`, `fix:`, `test:`, `docs:`).

The `rotini-mcp` command is itself a rotini program: its spec is
`cmd/rotini-mcp/.rotini.spec.yaml`. After changing the spec, regenerate with
`go generate ./cmd/rotini-mcp`.

## Linting

```bash
make lint
```

## Testing

```bash
make test              # run tests, including the end-to-end test that builds a rotini program
make test-fuzz         # run fuzz tests (60s per fuzzer)
make test-mutation     # run mutation tests
make test-race         # run tests with race detector
```

`go test -short ./...` skips the end-to-end test.

### Against an unreleased rotini

This module reads what rotini's generator writes, so a change often needs a rotini that isn't
released yet. With rotini checked out beside this repository, add a `go.work` (git ignores
it) that replaces the version `go.mod` requires:

```
go 1.27

use .

replace github.com/go-rotini/rotini <version> => ../rotini
```

Use `replace` rather than `use ../rotini`: the end-to-end test builds a program against the
rotini this module resolves, and finds a local checkout through the replacement. The mutation
test copies the module elsewhere, so it can't run under a `go.work`.

To regenerate `rotini-mcp` with that rotini, build its companion and run it where
`go generate` would:

```bash
go -C ../rotini build -o "$PWD/rotini.local" ./cmd/rotini
(cd cmd/rotini-mcp && ../../rotini.local generate ./.rotini.spec.yaml --config ./.rotini.conf.yaml)
rm rotini.local
```

## Pull Requests

- Keep PRs focused on a single change.
- Include tests that cover the change.
- Reference any relevant issues.

## Reporting Bugs

Open an issue with the tool definition (from `tools/mcp.json`, with its `_meta` facts), the
call's arguments, how the server runs, and the expected vs. actual result. Redact secret values —
replace them with `<redacted>`.

## Security

See [SECURITY.md](SECURITY.md) for reporting vulnerabilities.
