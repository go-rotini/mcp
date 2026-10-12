// Package mcp serves a rotini program's commands as Model Context Protocol tools.
//
// Rotini's tools feature writes the tool definitions (`tools/mcp.json`, and with `go: true`
// the generated `ToolsMCP` string): an MCP tools/list result whose tools each carry, under
// `_meta["dev.rotini/invoke"]`, what a server needs to turn a call back into a command line.
// This package reads that document and serves it, over stdio by default. Each call is checked
// against the tool's input schema, written as a command line ([Invoke.Argv]) and run as a
// child process; its stdout becomes the result, checked against the output schema.
//
// There are two ways to serve:
//
//   - The rotini-mcp command serves a tools file for a built program:
//     `rotini-mcp serve --tools tools/mcp.json --bin ./taskr`.
//   - A program serves its own tools from a command it declares (such as `taskr mcp`, kept from
//     agents with `agent: false`), whose handler calls [Serve] with ToolsMCP. Each call
//     re-runs the program's own executable.
//
// # Runs
//
// The program gets the server's environment (secrets and other env inputs come from there),
// stdin from the call's stdin parameter and never the server's own stdin, which is the MCP
// transport. A canceled call, or one past [WithTimeout], sends the process SIGTERM, so
// rotini's signal handling runs, and kills it after [WithGrace] (at once with a grace period
// of 0). On Windows the process is killed at once. Each of stdout and stderr is capped by
// [WithOutputLimit].
//
// # Results
//
// A zero exit is a success. When the tool declares output, stdout is its structured content
// (for a stream, one JSON value per line, as an array), checked against the output schema;
// text after the value, such as the default reporter's messages, goes in the result's _meta.
// Use rotini's StructuredReporter so stdout holds only the output. A non-zero exit is an
// error result whose text is stderr (stdout when stderr is empty); a StructuredReporter error line on stderr becomes its
// structured content, or, for a tool with an output schema (which clients check structured
// content against, even on an error result), its _meta["dev.rotini/error"].
package mcp
