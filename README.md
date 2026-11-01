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
- Structured results: stdout as `structuredContent` (an array for a streaming command), a
  `StructuredReporter` error line on stderr with an error result, and every reporter line in
  `_meta`
- Safe runs: secrets refused on the command line, the server's stdin never passed on, SIGTERM
  on cancel or timeout so the program's signal handling runs, capped output
- MCP revision 2026-07-28, with 2025-11-25 for older clients
- Selectors for tools and parameters, and middleware around each call
- `rotini-mcp add`, `remove` and `list` manage the server in the configuration of Claude Code,
  Claude Desktop, Cursor and VS Code
- Built on the official [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)

## Installation

```bash
go install github.com/go-rotini/mcp/cmd/rotini-mcp@latest   # the command
go get github.com/go-rotini/mcp                             # the library
```

Requires Go 1.27 or later and rotini v1.13.0 or later.

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
rotini's guide, "Agent-ready CLIs". A command's `examples` reach clients as its input schema's
`examples`, and its `metadata` as `_meta["dev.rotini/metadata"]`, both as written; the parameter
selector drops an example that sets a parameter it leaves out.

## Serving with rotini-mcp

```bash
rotini-mcp serve ./bin/taskr                       # tools/mcp.json, running ./bin/taskr for each call
rotini-mcp add claude-code ./bin/taskr             # add that server to Claude Code
rotini-mcp add claude-code ./bin/taskr mcp         # or: Claude Code runs "taskr mcp", a program serving itself
rotini-mcp list                                    # the servers in every client
rotini-mcp remove claude-code taskr
```

With a Rotini companion installed the same way (both with `go install`, or both as tools of the
module), the same commands run as `rotini mcp serve …`, `rotini mcp add …` and so on.

### serve

`rotini-mcp serve <program>` serves the program's tools on stdio. `<program>` is a path, or a
name on `PATH`.

| Flag | Default | |
|---|---|---|
| `--tools` | `tools/mcp.json` | the tools file the program's tools feature writes |
| `--env KEY=value` | | add a variable to the program's environment (repeatable) |
| `--tool NAME` | every tool | serve only these tools (repeatable) |
| `--timeout` | `0s` (none) | stop a call that runs longer |
| `--grace` | `5s` | time between SIGTERM and kill for a stopped call (`0` kills it at once) |
| `--output-limit` | `10Mi` | the most a call may write on each of stdout and stderr |
| `--concurrency` | `0` (none) | run at most this many calls at once; more wait their turn |
| `--revision` | `2026-07-28` | the newest MCP revision to serve for (`2025-11-25` for older clients) |
| `--success-stderr` | `content` | where a successful call's stderr goes: `content` shows it, `meta` keeps it in `_meta` only |

### add, remove, list

`rotini-mcp add <client> <program> [args...]` adds a server to a client's configuration:

- **With only the program,** the client runs `rotini-mcp serve <program> --tools <file>`.
- **With arguments after the program,** the client runs the program with them, for a program that
  serves its own tools (`rotini-mcp add claude-code ./bin/taskr mcp`). Put `--` before arguments
  that start with `-`.
- **The server is named after the program** (`taskr`); `--name` names it otherwise.
- **Paths are written as absolute paths,** since a client starts servers from a directory of its
  own; a bare program name is left for the client to find on its `PATH`.
- **The entry names the `rotini-mcp` that wrote it.** Run an installed one, or one the module
  declares as a tool: run from Go's build cache (`go tool`), it writes
  `go -C <module> tool rotini-mcp serve …`, so the module's pinned version runs and a cleaned
  cache doesn't break the entry. `go run` is refused for the same reason.

Clients: `claude-code` (the project's `.mcp.json`), `claude-desktop`, `cursor` and `vscode`
(`--scope user` or `project`).

- **Backup:** a file is backed up to `<file>.bak` before it is rewritten, replacing an earlier
  backup.
- **What's kept:** the rewrite keeps every other setting in its order, but not comments. Adding a
  server that's already there updates its command, arguments and env, and keeps its other
  settings (such as `disabled` or `autoApprove`).
- **Symbolic links:** a file that is a symbolic link stays one; the file it links to is rewritten.
- **Secrets:** `--env` values are written into the file, and a project's file is often committed,
  so keep secrets out of them.

`rotini-mcp remove <client> <name>` removes a server, by its name or by the program it was added
for. `rotini-mcp list [client]` lists the servers in every client's files, or one client's: the
client, the file's scope, the name and the command.

### Permissions

For Claude Code, `add --permissions` also writes rules for the server's tools into the project's
`.claude/settings.json`, from the tools' annotations:

| Annotation | Rule |
|---|---|
| read-only | runs without asking (`permissions.allow`) |
| destructive | always asks (`permissions.ask`) |
| neither | none; the client's default applies |

The rules are Claude Code's tool names, `mcp__<server>__<tool>`. Adding again replaces the
server's rules, every other rule is kept, and `remove` takes them away. A program serving
itself reads the annotations from `--tools` (default `tools/mcp.json`).

## Serving from the program itself

Declare a command for it, kept from agents so it isn't a tool:

```yaml
commands:
  - name: mcp
    summary: serve this program's commands over MCP
    agent: false
```

and serve the generated `ToolsMCP` from its handler (an error-returning handler, which
`hook_returns: error` on the conf's cmd package seeds):

```go
func (*taskrMcpHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	return mcp.Serve(ctx, []byte(ToolsMCP),
		mcp.WithStdio(rtx.Stdin, rtx.Stdout),
		mcp.WithEnv(rtx.Environ()),
		mcp.WithServingCommand("mcp"))
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
- **A success's messages** (stderr, and text after a JSON output) are shown, since clients
  don't show `_meta`: they are the text when stdout is empty and the tool declares no output,
  and a second text block otherwise. They are in `_meta["dev.rotini/stderr"]` too.
  `WithSuccessStderr(SuccessStderrMeta)` (`--success-stderr meta`) keeps them in `_meta` only.
- **A non-zero exit** is an error result: its text is stderr, and a `StructuredReporter` error
  line on stderr is its structured content. A tool with an output schema puts the error line in
  `_meta["dev.rotini/error"]` instead, since clients check structured content against the output
  schema even on an error result. `_meta["dev.rotini/exit_code"]` has the exit code.
- **Every `StructuredReporter` line** on stderr (errors, warnings, infos, successes and the
  records of channels the spec declares, such as `{"schema_version":1,"skipped":{…}}`) is in the
  result's `_meta["dev.rotini/records"]`, in order, on success and on error.
- **A canceled call**, one past the timeout or its context's deadline (a middleware may set one),
  or one past the output limit sends SIGTERM to the program and every process it started (they
  share a process group), and kills them after the grace period, or once they have all exited
  (at once with a grace period of 0). A canceled call ends with the cancellation as an error;
  otherwise the result's text ends with the last few KiB the program wrote on stderr (or
  stdout). On plan9, js and wasip1 only the program itself is stopped. Windows can't send
  SIGTERM, so there the program and the processes it started (a job object holds them) are
  killed at once. There is no timeout unless one is set.
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
| `WithSuccessStderr(mode)` | show a success's stderr (`SuccessStderrContent`, default) or keep it in `_meta` (`SuccessStderrMeta`) |
| `WithServingCommand(path...)` | refuse a tool that would run the serving command |
| `WithImplementation(name, version)` / `WithLog(w)` | the server's name; its log |

`NewServer` returns the SDK server for other transports, `ParseTools` reads a tools document,
and `Invoke.Argv` builds a call's command line.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
