# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability, please report it responsibly by emailing **matthewcgetz@gmail.com**. Do not open a public issue.

You should receive a response within 72 hours. If accepted, a fix will be developed privately and released as a patch version.

## What a Server Runs

A server runs one program: the `--bin` given to `rotini-mcp serve`, or the running program
itself in the library form. Each call runs it with a command line built from the call's
arguments; nothing goes through a shell. On Windows, `rotini-mcp` refuses a batch file
(`.bat`, `.cmd`) as `--bin`: Windows runs it through cmd.exe, which parses its arguments again,
so a call's values could run as commands.

- **Arguments are checked before anything runs.** A call must match the tool's input schema
  (JSON Schema 2020-12, validated with [go-rotini/jsonschema](https://github.com/go-rotini/jsonschema)),
  or it is an error result and the program never starts.
- **Positionals come after `--`**, so a value that looks like a flag (`--delete-all`) stays a
  value. Flag values are attached (`--name=value`) for the same reason. The exception is a
  passthrough command, which the author opts in for agents: it reads every word after its name
  as typed, so its words are passed raw, and the program decides what they mean. When the
  program reads response files, a word starting with the prefix has it doubled, so a word like
  `@/etc/hosts` is passed as written and never read as a file.
- **Secrets are refused.** A parameter the tools document marks secret is never written on the
  command line, where other users of the machine can read it (`ps`). Supply secrets through the
  server's environment, which each run inherits.
- **What agents can reach is the program author's decision.** Rotini's tools feature leaves
  out hidden, deprecated, secret and `from:` (file-reading) inputs and passthrough commands
  unless the author opts them in with `agent: true`, and always leaves out the directory flag.
  This module serves what the document contains; `WithSelector` and `WithParameterSelector`
  (`--tool` on the command line) narrow it further.
- **The server's stdin is never given to a run.** It is the MCP transport; a run's stdin is the
  call's `stdin` parameter, or empty.

## Resource Limits

- **Output** is capped per run on each of stdout and stderr (10 MiB by default,
  `WithOutputLimit` / `--output-limit`). A run that writes more is stopped and the call fails.
- **Time** is unbounded by default; `WithTimeout` / `--timeout` stops a run that takes longer.
- **Stopping a run** sends SIGTERM, so a rotini program's signal handling runs, then kills it
  after a grace period (5s, `WithGrace` / `--grace`; 0 kills it at once). Windows can't send
  SIGTERM; there the process is killed at once.
- **Requests** are framed by the [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk),
  whose stdio transport bounds the size of one message.

## Client Configuration Files

`rotini-mcp config` and the `clients` package change a client's configuration file only when
asked. An existing file is copied to `<file>.bak` before it is rewritten, and the new file
replaces it in one step, keeping its mode. `--env` values are written into the file as plain
text; a project's file is often committed, so supply secrets another way.
