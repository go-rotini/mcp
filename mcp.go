package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/go-rotini/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Call is one tool call on its way to the program, as middleware sees it. Middleware may change
// Argv, Stdin and Env before the program runs.
type Call struct {
	Tool      Tool
	Arguments map[string]any // as the client sent them, numbers as json.Number
	Argv      []string       // the command line, without the program name
	Stdin     []byte
	Env       []string
}

// Handler runs a call and returns its result.
type Handler func(ctx context.Context, c *Call) (*sdk.CallToolResult, error)

// Middleware wraps the handler that runs the program, for logging, limits or policy. It sees a
// call after its arguments are checked and its command line is built.
type Middleware func(next Handler) Handler

// Option configures [Serve] and [NewServer].
type Option func(*config)

type config struct {
	exe            string
	env            []string
	envSet         bool
	dir            string
	timeout, grace time.Duration
	in             io.Reader
	out            io.Writer
	selector       func(Tool) bool
	params         func(t Tool, param string) bool
	middleware     []Middleware
	outputLimit    int64
	revision       string
	name, version  string
	serving        []string
	logger         io.Writer
}

// WithExecutable runs path for each call. By default it is the running program
// (os.Executable), so a program can serve its own tools.
func WithExecutable(path string) Option {
	return func(c *config) { c.exe = path }
}

// WithEnv sets the environment each run gets (KEY=value entries). By default it is the server's
// own, which is where secrets and other env inputs come from.
func WithEnv(env []string) Option {
	return func(c *config) { c.env, c.envSet = slices.Clone(env), true }
}

// WithDir runs the program in dir. By default it runs in the server's working directory.
func WithDir(dir string) Option {
	return func(c *config) { c.dir = dir }
}

// WithTimeout stops a run that takes longer than d (zero, the default, for no limit).
func WithTimeout(d time.Duration) Option {
	return func(c *config) { c.timeout = d }
}

// WithGrace sets how long a stopped run has between SIGTERM and being killed (default 5s). Zero
// or less kills a stopped run at once, with no SIGTERM.
func WithGrace(d time.Duration) Option {
	return func(c *config) { c.grace = d }
}

// WithStdio serves over in and out instead of the process's stdin and stdout. In a rotini
// handler, pass rtx.Stdin and rtx.Stdout; they must be the real streams the client connects to.
func WithStdio(in io.Reader, out io.Writer) Option {
	return func(c *config) { c.in, c.out = in, out }
}

// WithSelector serves only the tools keep returns true for.
func WithSelector(keep func(Tool) bool) Option {
	return func(c *config) { c.selector = keep }
}

// WithParameterSelector serves only the parameters keep returns true for. Leaving out a
// required parameter is an error.
func WithParameterSelector(keep func(t Tool, param string) bool) Option {
	return func(c *config) { c.params = keep }
}

// WithMiddleware wraps each call's handler; the first middleware given is the outermost.
func WithMiddleware(mw ...Middleware) Option {
	return func(c *config) { c.middleware = append(c.middleware, mw...) }
}

// WithOutputLimit caps each run's stdout and stderr at n bytes (default 10 MiB). A run that
// writes more is stopped, and the call is an error. Zero or less means no limit.
func WithOutputLimit(n int64) Option {
	return func(c *config) { c.outputLimit = n }
}

// The MCP revisions a tools document can be served for.
const (
	Revision20260728 = "2026-07-28"
	Revision20251125 = "2025-11-25"
)

// WithRevision serves the tools for an MCP revision: [Revision20260728] (the default) or
// [Revision20251125], which needs every output to be an object, so a tool whose output isn't one
// gets {"result": …}. Prefer writing the tools document for the revision you serve (the tools
// feature's mcp_revision); this converts one written for the newer revision.
func WithRevision(rev string) Option {
	return func(c *config) { c.revision = rev }
}

// WithImplementation names the server to clients. By default it is the executable's base name
// and the main module's version.
func WithImplementation(name, version string) Option {
	return func(c *config) { c.name, c.version = name, version }
}

// WithServingCommand names the command that serves the tools, by its path below the root, so a
// tool that would run it (and start another server) is refused. Give it in the library form;
// the serving command should also be kept from agents with `agent: false`.
func WithServingCommand(path ...string) Option {
	return func(c *config) { c.serving = slices.Clone(path) }
}

// WithLog writes the server's log (bad tool names, protocol problems) to w. By default nothing
// is logged; never log to the stdout the server speaks on.
func WithLog(w io.Writer) Option {
	return func(c *config) { c.logger = w }
}

// Serve serves the tools document over MCP on stdio until the client disconnects or ctx is
// done. tools is the `tools/mcp.json` Rotini's tools feature writes, or the generated `ToolsMCP`
// string; each call runs the program with the command line its arguments make.
//
// In a rotini program, a command can serve the program's own tools:
//
//	func (*mcpHandler) Run(ctx context.Context, rtx *rotini.Context) {
//		err := mcp.Serve(ctx, []byte(ToolsMCP), mcp.WithStdio(rtx.Stdin, rtx.Stdout), mcp.WithServingCommand("mcp"))
//		if err != nil {
//			rtx.HaltWith(err)
//		}
//	}
func Serve(ctx context.Context, tools []byte, opts ...Option) error {
	s, cfg, err := newServer(tools, opts)
	if err != nil {
		return err
	}
	var transport sdk.Transport = &sdk.StdioTransport{}
	if cfg.in != nil || cfg.out != nil {
		if cfg.in == nil || cfg.out == nil {
			return errors.New("mcp: WithStdio needs both a reader and a writer")
		}
		transport = &sdk.IOTransport{Reader: io.NopCloser(cfg.in), Writer: transportWriter{cfg.out}}
	}
	err = s.Run(ctx, transport)
	if err != nil && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return nil // stopped on purpose (a signal, or the caller), not a failure
	}
	if err != nil {
		return fmt.Errorf("mcp: %w", err)
	}
	return nil
}

// NewServer builds the MCP server for a tools document, for serving over another transport
// (sdk.Server.Run, or the SDK's HTTP handlers).
func NewServer(tools []byte, opts ...Option) (*sdk.Server, error) {
	s, _, err := newServer(tools, opts)
	return s, err
}

func newServer(doc []byte, opts []Option) (*sdk.Server, *config, error) {
	cfg := &config{grace: defaultGrace, outputLimit: defaultOutputLimit, revision: Revision20260728}
	for _, o := range opts {
		if o != nil {
			o(cfg)
		}
	}
	if cfg.revision != Revision20260728 && cfg.revision != Revision20251125 {
		return nil, nil, fmt.Errorf("mcp: unknown MCP revision %q; use %s or %s", cfg.revision, Revision20260728, Revision20251125)
	}
	exe := cfg.exe
	if exe == "" {
		self, err := os.Executable()
		if err != nil {
			return nil, nil, fmt.Errorf("mcp: find the running program: %w", err)
		}
		exe = self
	}
	tools, err := ParseTools(doc)
	if err != nil {
		return nil, nil, err
	}
	srv := &server{cfg: cfg, exe: exe}
	name, version := cfg.name, cfg.version
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(exe), ".exe")
	}
	if version == "" {
		version = moduleVersion()
	}
	sopts := &sdk.ServerOptions{Capabilities: &sdk.ServerCapabilities{}}
	if cfg.logger != nil {
		sopts.Logger = newLogger(cfg.logger)
	}
	s := sdk.NewServer(&sdk.Implementation{Name: name, Version: version}, sopts)
	for _, t := range tools {
		if cfg.selector != nil && !cfg.selector(t) {
			continue
		}
		if cfg.serving != nil && slices.Equal(t.Invoke.Path, cfg.serving) {
			return nil, nil, fmt.Errorf("mcp: the tool %q runs the serving command %q, which would start another server; keep it from agents with `agent: false`", t.Name, strings.Join(cfg.serving, " "))
		}
		ct, err := srv.prepare(t)
		if err != nil {
			return nil, nil, err
		}
		def, err := ct.definition()
		if err != nil {
			return nil, nil, err
		}
		s.AddTool(def, srv.handler(ct))
	}
	return s, cfg, nil
}

// server runs the calls of one MCP server.
type server struct {
	cfg *config
	exe string
}

// tool is a tool ready to serve: its schemas compiled.
type tool struct {
	Tool

	in, out *jsonschema.Schema
}

// prepare applies the parameter selector and the revision to a tool and compiles its schemas.
func (s *server) prepare(t Tool) (*tool, error) {
	t.input = jsonCopy(t.input)
	if s.cfg.params != nil {
		if err := selectParameters(&t, s.cfg.params); err != nil {
			return nil, err
		}
	}
	if t.output != nil {
		t.output = jsonCopy(t.output)
		if s.cfg.revision == Revision20251125 {
			wrapOutput(&t)
		}
	}
	in, err := compile(t.Name, "input", t.input)
	if err != nil {
		return nil, err
	}
	ct := &tool{Tool: t, in: in}
	if t.output != nil {
		if ct.out, err = compile(t.Name, "output", t.output); err != nil {
			return nil, err
		}
	}
	return ct, nil
}

// selectParameters drops the parameters keep refuses from a tool's input schema and facts.
func selectParameters(t *Tool, keep func(Tool, string) bool) error {
	props, _ := t.input["properties"].(map[string]any)
	required, _ := t.input["required"].([]any)
	for _, name := range t.Parameters() {
		if keep(*t, name) {
			continue
		}
		if slices.Contains(required, any(name)) {
			return fmt.Errorf("mcp: tool %q: the parameter %q is required, so it can't be left out", t.Name, name)
		}
		delete(props, name)
	}
	return nil
}

// handler serves one tool's calls: check the arguments, build the command line, run the
// middleware and the program, and turn the run into a result.
func (s *server) handler(t *tool) sdk.ToolHandler {
	run := Handler(func(ctx context.Context, c *Call) (*sdk.CallToolResult, error) {
		r, err := s.execute(ctx, c)
		if err != nil {
			return nil, err
		}
		return t.result(r), nil
	})
	for _, mw := range slices.Backward(s.cfg.middleware) {
		run = mw(run)
	}
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		raw := bytes.TrimSpace(req.Params.Arguments)
		if len(raw) == 0 || string(raw) == "null" {
			raw = []byte("{}")
		}
		var args map[string]any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber() // keep large and exact numbers as typed on the command line
		if dec.Decode(&args) != nil || args == nil {
			return errorResult(nil, "the arguments aren't a JSON object"), nil //nolint:nilerr // a bad call is an error result, not a protocol error
		}
		// A null parameter is one left out: a command line has no spelling for null.
		maps.DeleteFunc(args, func(_ string, v any) bool { return v == nil })
		checked, err := json.Marshal(args)
		if err != nil {
			return errorResult(nil, err.Error()), nil
		}
		if msg := validationMessage(t.in, checked); msg != "" {
			return errorResult(nil, "the arguments don't match the tool's input schema:\n"+msg), nil
		}
		argv, stdin, err := t.Invoke.Argv(args)
		if err != nil {
			return errorResult(nil, err.Error()), nil
		}
		env := s.cfg.env
		if !s.cfg.envSet {
			env = os.Environ()
		}
		c := &Call{Tool: t.Tool, Arguments: args, Argv: argv, Stdin: stdin, Env: slices.Clone(env)}
		return run(ctx, c)
	}
}

// moduleVersion is the main module's version, for the server's name.
func moduleVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(devel)"
}

// transportWriter is the writer WithStdio gives: it never closes the stream, and flushes a
// buffered one (such as rotini's buffered stdout) after each message.
type transportWriter struct{ w io.Writer }

func (t transportWriter) Write(p []byte) (int, error) {
	n, err := t.w.Write(p)
	if f, ok := t.w.(interface{ Flush() error }); ok && err == nil {
		err = f.Flush()
	}
	return n, err //nolint:wrapcheck // the transport reports the stream's own error
}

func (transportWriter) Close() error { return nil }
