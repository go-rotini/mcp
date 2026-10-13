package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"runtime"
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
	Arguments map[string]any // as the client sent them, numbers as json.Number, nulls removed
	Argv      []string       // the command line, without the program name
	Stdin     []byte         // what the program reads on stdin
	Env       []string       // the program's environment, KEY=value entries
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
	maxConcurrent  int
}

// WithExecutable runs path for each call. By default it is the running program
// ([os.Executable]), so a program can serve its own tools.
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

// WithParameterSelector serves only the parameters keep returns true for: the others are left
// out of the tool's input schema, and a call that passes one is refused, whatever the schema
// says about extra properties. Leaving out a required parameter is an error.
func WithParameterSelector(keep func(t Tool, param string) bool) Option {
	return func(c *config) { c.params = keep }
}

// WithMiddleware wraps each call's handler; the first middleware given is the outermost.
func WithMiddleware(mw ...Middleware) Option {
	return func(c *config) { c.middleware = append(c.middleware, mw...) }
}

// WithMaxConcurrent runs at most n calls' programs at once; a call beyond that waits for a
// running one to finish, or for the client to cancel it. Zero or less, the default, means no
// limit.
func WithMaxConcurrent(n int) Option {
	return func(c *config) { c.maxConcurrent = n }
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
//
// The revision is the newest a client is served for. A client that negotiates an older revision
// than [Revision20260728] is always served the {"result": …} form, in tools/list and in each
// result. Every client that starts with initialize negotiates an older revision,
// whatever it asks for.
func WithRevision(rev string) Option {
	return func(c *config) { c.revision = rev }
}

// WithImplementation names the server to clients. By default it is the executable's base name
// and the main module's version.
func WithImplementation(name, version string) Option {
	return func(c *config) { c.name, c.version = name, version }
}

// WithServingCommand names the command that serves the tools, by its command names below the
// root as a tool's [Invoke.Path] gives them (such as "mcp"). Serving a tool that would run it,
// and so start another server, is an error. The serving command should also be kept from agents
// with `agent: false`.
func WithServingCommand(path ...string) Option {
	return func(c *config) { c.serving = slices.Clone(path) }
}

// WithLog writes the server's log (bad tool names, protocol problems) to w. By default nothing
// is logged; never log to the stdout the server speaks on.
func WithLog(w io.Writer) Option {
	return func(c *config) { c.logger = w }
}

// Serve serves the tools document over MCP on stdio (the process's own, or [WithStdio]'s)
// until the client disconnects or ctx is done; a stop because ctx is done returns nil. tools is the `tools/mcp.json` Rotini's tools feature writes, or the generated `ToolsMCP`
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
// ([sdk.Server.Run], or the SDK's HTTP handlers). [WithStdio] has no effect on it.
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
	if err := refuseBatchFile(runtime.GOOS, exe); err != nil {
		return nil, nil, err
	}
	tools, err := ParseTools(doc)
	if err != nil {
		return nil, nil, err
	}
	srv := &server{cfg: cfg, exe: exe, older: map[string]*sdk.Tool{}}
	if cfg.maxConcurrent > 0 {
		srv.slots = make(chan struct{}, cfg.maxConcurrent)
	}
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
		srv.log = sopts.Logger
	}
	s := sdk.NewServer(&sdk.Implementation{Name: name, Version: version}, sopts)
	for _, t := range tools {
		if cfg.selector != nil && !cfg.selector(t) {
			continue
		}
		if cfg.serving != nil && slices.Equal(t.Invoke.Path, cfg.serving) {
			return nil, nil, fmt.Errorf("mcp: the tool %q runs the serving command %q, which would start another server; keep it from agents with `agent: false`", t.Name, strings.Join(cfg.serving, " "))
		}
		newer, older, err := srv.prepare(t)
		if err != nil {
			return nil, nil, err
		}
		def, err := newer.definition()
		if err != nil {
			return nil, nil, err
		}
		if older != newer {
			if srv.older[t.Name], err = older.definition(); err != nil {
				return nil, nil, err
			}
		}
		if err := addTool(s, def, srv.handler(newer, older)); err != nil {
			return nil, nil, err
		}
	}
	if len(srv.older) > 0 {
		s.AddReceivingMiddleware(srv.listForRevision)
	}
	return s, cfg, nil
}

// addTool adds a tool to the server, returning as an error the panic the SDK raises for a
// definition it rejects.
func addTool(s *sdk.Server, def *sdk.Tool, h sdk.ToolHandler) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("mcp: tool %q: %v", def.Name, r)
		}
	}()
	s.AddTool(def, h)
	return nil
}

// refuseBatchFile refuses a Windows batch file: Windows runs it through cmd.exe, which parses
// its arguments again, so a call's values could run as commands.
func refuseBatchFile(goos, path string) error {
	if goos != "windows" {
		return nil
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".bat", ".cmd":
		return fmt.Errorf("mcp: %s is a batch file, which runs through cmd.exe, which parses its arguments again, so a call's values could run as commands; run the program itself", path)
	}
	return nil
}

// server runs the calls of one MCP server.
type server struct {
	cfg *config
	exe string
	// older holds, by tool name, the definition served to a client of a revision before
	// 2026-07-28, for each tool whose definition differs for it.
	older map[string]*sdk.Tool
	// slots holds a token for each running program, when the number at once is limited.
	slots chan struct{}
	log   *slog.Logger // WithLog's logger, or nil
}

// tool is a tool ready to serve: its schemas compiled, and the parameters the selector left out.
type tool struct {
	Tool

	in, out *jsonschema.Schema
	hidden  map[string]bool
}

// prepare applies the parameter selector and the revision to a tool and compiles its schemas.
// newer is the tool as served for the configured revision; older is the tool as served to a
// client of a revision before 2026-07-28, where every output is an object. They are the same
// tool when the two forms don't differ.
func (s *server) prepare(t Tool) (newer, older *tool, err error) {
	t.input = jsonCopy(t.input)
	var hidden map[string]bool
	if s.cfg.params != nil {
		if hidden, err = selectParameters(&t, s.cfg.params); err != nil {
			return nil, nil, err
		}
	}
	in, err := compile(t.Name, "input", t.input)
	if err != nil {
		return nil, nil, err
	}
	if t.output == nil {
		newer = &tool{Tool: t, in: in, hidden: hidden}
		return newer, newer, nil
	}
	t.output = jsonCopy(t.output)
	wrapped := t
	wrapped.output = jsonCopy(t.output)
	wrapOutput(&wrapped)
	if older, err = compileOutput(wrapped, in, hidden); err != nil {
		return nil, nil, err
	}
	if s.cfg.revision == Revision20251125 {
		return older, older, nil
	}
	if newer, err = compileOutput(t, in, hidden); err != nil {
		return nil, nil, err
	}
	if !wrapped.Invoke.Wrapped && t.output["type"] == "object" {
		older = newer // an object output reads the same in both revisions
	}
	return newer, older, nil
}

func compileOutput(t Tool, in *jsonschema.Schema, hidden map[string]bool) (*tool, error) {
	out, err := compile(t.Name, "output", t.output)
	if err != nil {
		return nil, err
	}
	return &tool{Tool: t, in: in, out: out, hidden: hidden}, nil
}

// selectParameters drops the parameters keep refuses from a tool's input schema, and returns
// them.
func selectParameters(t *Tool, keep func(Tool, string) bool) (map[string]bool, error) {
	props, _ := t.input["properties"].(map[string]any)
	required, _ := t.input["required"].([]any)
	hidden := map[string]bool{}
	for _, name := range t.Parameters() {
		if keep(*t, name) {
			continue
		}
		if slices.Contains(required, any(name)) {
			return nil, fmt.Errorf("mcp: tool %q: the parameter %q is required, so it can't be left out", t.Name, name)
		}
		delete(props, name)
		hidden[name] = true
	}
	return hidden, nil
}

// olderRevision reports whether a request follows an MCP revision before 2026-07-28. A
// request of 2026-07-28 or later names its revision in its own _meta. One that names none
// belongs to a session that began with initialize, which never settles on 2026-07-28 or
// later whatever the client asked for, so it follows an older revision. A request with
// neither follows the newest.
func olderRevision(meta map[string]any, session *sdk.ServerSession) bool {
	if v, ok := meta[sdk.MetaKeyProtocolVersion].(string); ok {
		return v < Revision20260728
	}
	return session != nil && session.InitializeParams() != nil
}

// listMeta is a tools/list request's _meta, which a request without params lacks.
func listMeta(r *sdk.ListToolsRequest) map[string]any {
	if r.Params == nil {
		return nil
	}
	return r.Params.GetMeta()
}

// listForRevision serves tools/list to a client of a revision before 2026-07-28 with the older
// definitions of the tools that have one.
func (s *server) listForRevision(next sdk.MethodHandler) sdk.MethodHandler {
	return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
		res, err := next(ctx, method, req)
		lr, isList := res.(*sdk.ListToolsResult)
		r, isReq := req.(*sdk.ListToolsRequest)
		if err != nil || !isList || !isReq || !olderRevision(listMeta(r), r.Session) {
			return res, err
		}
		out := *lr
		out.Tools = make([]*sdk.Tool, len(lr.Tools))
		for i, t := range lr.Tools {
			out.Tools[i] = t
			if o, ok := s.older[t.Name]; ok {
				served := *t
				served.OutputSchema = o.OutputSchema
				out.Tools[i] = &served
			}
		}
		return &out, nil
	}
}

// handler serves one tool's calls: check the arguments, build the command line, run the
// middleware and the program, and turn the run into a result, in the form the request's
// revision reads (older for one before 2026-07-28).
func (s *server) handler(newer, older *tool) sdk.ToolHandler {
	runFor := func(t *tool) Handler {
		run := Handler(func(ctx context.Context, c *Call) (*sdk.CallToolResult, error) {
			if s.slots != nil {
				select {
				case s.slots <- struct{}{}:
					defer func() { <-s.slots }()
				case <-ctx.Done():
					return nil, fmt.Errorf("mcp: %w", context.Cause(ctx))
				}
			}
			r, err := s.execute(ctx, c)
			if err != nil {
				return nil, err
			}
			return t.result(r), nil
		})
		for _, mw := range slices.Backward(s.cfg.middleware) {
			run = mw(run)
		}
		return run
	}
	runNewer, runOlder := runFor(newer), runFor(newer)
	if older != newer {
		runOlder = runFor(older)
	}
	return func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		t, run := newer, runNewer
		if olderRevision(req.Params.GetMeta(), req.Session) {
			t, run = older, runOlder
		}
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
		for _, name := range slices.Sorted(maps.Keys(args)) {
			if t.hidden[name] {
				return errorResult(nil, (&ArgvError{Param: name, Reason: "the tool has no such parameter"}).Error()), nil
			}
		}
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

// moduleVersion is the main module's version, for the server's implementation version.
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
