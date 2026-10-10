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
