# go-rotini/mcp

Serve a [rotini](https://github.com/go-rotini/rotini) program's commands as
[Model Context Protocol](https://modelcontextprotocol.io) tools, so an AI agent can call them.

Rotini's `tools` feature writes the tool definitions: `tools/mcp.json`, an MCP `tools/list`
result built from the program's spec, with each command's effects as annotations and, under
`_meta["dev.rotini/invoke"]`, what a server needs to turn a call back into a command line. This
module serves that file. Each call is checked against the tool's input schema, written as a
command line and run as a child process; what the program prints becomes the result.

## Features

- Two ways to serve: the `rotini-mcp` command serves a tools file for any built program, and a
  program can serve its own tools from a command it declares (`taskr mcp`)
- Command lines built the way rotini reads them: attached `--flag=value`, lists and maps by
  repeating the flag, counts by repeating, `--no-flag` for false on a negatable bool (else
  `--flag=false`), `--` before positionals so a value that looks like a flag stays a value, `@@`
  for a literal `@` on a parameter that reads `@file`
- Arguments validated against the input schema (JSON Schema 2020-12) before anything runs;
  output checked against the output schema
- Structured results: stdout as `structuredContent` (an array for a streaming command), and a
  `StructuredReporter` error line on stderr with an error result
- Safe runs: secrets refused on the command line, the server's stdin never passed on, SIGTERM
  on cancel or timeout so the program's signal handling runs, capped output
- MCP revision 2026-07-28, with 2025-11-25 for older clients
- Selectors for tools and parameters, and middleware around each call
- `rotini-mcp config` writes the server into Claude Desktop, Claude Code, Cursor and VS Code
  configuration
- Built on the official [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)

## Installation

```bash
go install github.com/go-rotini/mcp/cmd/rotini-mcp@latest   # the command
go get github.com/go-rotini/mcp                             # the library
```

Requires Go 1.27 or later and rotini v1.4.3 or later.

## Writing the tools file

Turn on the `tools` feature in the program's `.rotini.conf.yaml` and run `rotini generate`:

```yaml
generate:
  features:
    - type: tools
      enabled: true
      go: true       # also generate var ToolsMCP, for a program that serves itself
```

Declare what each command does (`effects`), which flag selects JSON output
(`role: machine-output`), and keep commands and inputs from agents with `agent: false`. See
rotini's guide, "Agent-ready CLIs".

## Serving with rotini-mcp

```bash
rotini-mcp serve --tools tools/mcp.json --bin ./taskr
```

| Flag | Default | |
|---|---|---|
| `--tools` | | the tools file (required) |
| `--bin` | | the program to run for each call; a path, or a name on `PATH` (required) |
| `--env KEY=value` | | add a variable to the program's environment (repeatable) |
| `--tool NAME` | every tool | serve only these tools (repeatable) |
| `--timeout` | `0s` (none) | stop a call that runs longer |
| `--grace` | `5s` | time between SIGTERM and kill for a stopped call (`0` kills it at once) |
| `--output-limit` | `10Mi` | the most a call may write on each of stdout and stderr |
| `--concurrency` | `0` (none) | run at most this many calls at once; more wait their turn |
| `--revision` | `2026-07-28` | the MCP revision to serve for (`2025-11-25` for older clients) |

Add it to a client:

```bash
rotini-mcp config enable claude-desktop taskr --tools tools/mcp.json --bin ./taskr
rotini-mcp config enable claude-code taskr --command taskr --arg mcp   # a program serving itself
rotini-mcp config list cursor --scope project
rotini-mcp config disable vscode taskr
```

Clients: `claude-desktop`, `claude-code` (the project's `.mcp.json`), `cursor` and `vscode`
(`--scope user` or `project`). A file is backed up to `<file>.bak` before it is rewritten,
replacing an earlier backup; the rewrite keeps every other setting in its order, but comments are
not kept. Enabling a server that is already there updates its command, arguments and env and keeps
its other settings (such as `disabled` or `autoApprove`). A file that is a symbolic link stays one:
the file it links to is rewritten.
`--env` values are written into the file, and a project's file is often committed, so keep
secrets out of them. The entry names the `rotini-mcp` executable that wrote it, so run an
installed one, not one from `go run`.

## Serving from the program itself

Declare a command for it, kept from agents so it isn't a tool:

```yaml
commands:
  - name: mcp
    summary: serve this program's commands over MCP
    agent: false
```

and serve the generated `ToolsMCP` from its handler:

```go
func (*taskrMcpHandler) Run(ctx context.Context, rtx *rotini.Context) {
	err := mcp.Serve(ctx, []byte(ToolsMCP),
		mcp.WithStdio(rtx.Stdin, rtx.Stdout),
		mcp.WithEnv(rtx.Environ()),
		mcp.WithServingCommand("mcp"))
	if err != nil {
		rtx.HaltWith(err)
	}
}
```

Each call re-runs the program's own executable. `rtx.Stdin` and `rtx.Stdout` are the MCP
transport, so the handler must write nothing else to stdout. Rotini's signal handling cancels
`ctx` on SIGTERM, and `Serve` returns cleanly.

## Calls and results

- **Arguments** must match the tool's input schema, or the call is an error result and nothing
  runs. A `null` parameter counts as left out.
- **The command line** is the command's path, the tool's fixed flags (such as `--format=json`
  from the machine-output role), one attached occurrence per flag value in parameter-name order,
  then `--` and the positionals. A passthrough command reads every word after its name as typed,
  so its flags come before its name and its words follow it with no `--`; when the program reads
  response files, a word starting with the prefix has it doubled. A count of 0 writes nothing,
  so it is refused on a count flag with a default, and an empty list or map has no spelling on
  a flag without a separator, so it is refused too. A secret parameter is refused: supply
  secrets through the server's environment, which each run gets.
- **Stdin** is the `stdin` parameter for a command that reads stdin, and empty otherwise.
- **A zero exit** is a success. With an output schema, stdout's JSON is the structured content
  and its text; text after it (such as the default reporter's messages) goes in the result's
  `_meta["dev.rotini/trailing_stdout"]`. Use rotini's `StructuredReporter` so stdout holds only
  the output. Without an output schema, stdout is the text.
- **A non-zero exit** is an error result: its text is stderr, and a `StructuredReporter` error
  line on stderr is its structured content. A tool with an output schema puts the error line in
  `_meta["dev.rotini/error"]` instead, since clients check structured content against the output
  schema even on an error result. `_meta["dev.rotini/exit_code"]` has the exit code.
- **A canceled call**, one past the timeout or one past the output limit sends SIGTERM to the
  program and every process it started (they share a process group), and kills them after the
  grace period (at once with a grace period of 0). The result's text ends with the last few KiB
  the program wrote on stderr (or stdout). Windows can't send SIGTERM, so there the program and
  the processes it started (a job object holds them) are killed at once. There is no timeout
  unless one is set.
- **A program that can't be started**, a parameter left out by the parameter selector and a
  value holding a NUL byte are error results, not protocol errors.
- **Revisions:** a client that negotiates a revision before 2026-07-28 is served the
  `{"result": …}` form of a tool whose output isn't an object, in `tools/list` and in results.
  That is every client that starts with `initialize`, whatever revision it asks for, since
  `initialize` never settles on 2026-07-28 or later.
- **A background process** the program started that keeps its stdout or stderr open doesn't
  hold the call: once the program exits, the result has the output read within the grace period
  (a second with a grace period of 0) and the program's exit status. Such a process keeps
  running unless the call was stopped.

## Library options

| Option | |
|---|---|
| `WithExecutable(path)` | the program to run (default: the running program) |
| `WithEnv(env)` / `WithDir(dir)` | each run's environment and directory (default: the server's) |
| `WithTimeout(d)` / `WithGrace(d)` | stop long runs; time between SIGTERM and kill (0: kill at once) |
| `WithOutputLimit(n)` | cap each of stdout and stderr per run |
| `WithMaxConcurrent(n)` | run at most n calls at once; more wait their turn |
| `WithStdio(in, out)` | serve on these streams instead of the process's stdin and stdout |
| `WithSelector(fn)` / `WithParameterSelector(fn)` | serve only some tools or parameters |
| `WithMiddleware(mw...)` | wrap each call, with its command line built, for logging or policy |
| `WithRevision(rev)` | `Revision20260728` (default) or `Revision20251125` |
| `WithServingCommand(path...)` | refuse a tool that would run the serving command |
| `WithImplementation(name, version)` / `WithLog(w)` | the server's name; its log |

`NewServer` returns the SDK server for other transports, `ParseTools` reads a tools document,
and `Invoke.Argv` builds a call's command line.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
