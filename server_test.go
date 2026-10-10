package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The test binary doubles as the program the server runs: with fakeEnv set it acts as a small
// CLI (fakeMain) instead of running the tests.
const fakeEnv = "ROTINI_MCP_FAKE"

func TestMain(m *testing.M) {
	if os.Getenv(fakeEnv) == "1" {
		os.Exit(fakeMain(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func fakeMain(args []string) int {
	if len(args) == 0 {
		return 2
	}
	switch args[0] {
	case "echo":
		stdin, _ := io.ReadAll(os.Stdin)
		b, _ := json.Marshal(map[string]any{"argv": args[1:], "stdin": string(stdin), "env": os.Getenv("FAKE_VAR")})
		fmt.Println(string(b))
		fmt.Println("done")
	case "stream":
		for i := range 3 {
			fmt.Printf("{\"n\":%d}\n", i)
		}
		fmt.Println("streamed 3 items")
	case "array":
		fmt.Println("[1,2]")
	case "fail":
		fmt.Fprintln(os.Stderr, "Error: boom")
		fmt.Fprintln(os.Stderr, `{"schema_version":1,"error":{"message":"boom","category":"usage"}}`)
		return 3
	case "sleep":
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM)
		fmt.Fprintln(os.Stderr, "ready")
		select {
		case <-sig:
			_ = os.WriteFile(os.Getenv("FAKE_SIGNAL_FILE"), []byte("TERM"), 0o600)
			return 143
		case <-time.After(30 * time.Second):
		}
	case "flood":
		chunk := strings.Repeat("x", 1<<10)
		for range 1 << 12 {
			fmt.Print(chunk)
		}
	case "badout":
		fmt.Println(`{"ok":"yes"}`)
	case "plain":
		fmt.Println("hello")
		fmt.Fprintln(os.Stderr, "a warning")
	case "orphan":
		// A background process that outlives the program and keeps its stdout open.
		child := exec.Command("sleep", "10")
		child.Stdout = os.Stdout
		if child.Start() != nil {
			return 2
		}
		fmt.Println("started")
	default:
		return 2
	}
	return 0
}

// connect serves doc to an in-memory client, with the test binary as the program.
func connect(t *testing.T, doc []byte, opts ...Option) *sdk.ClientSession {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	base := []Option{WithExecutable(exe), WithEnv(append(os.Environ(), fakeEnv+"=1", "FAKE_VAR=from-env")), WithGrace(2 * time.Second)}
	s, err := NewServer(doc, append(base, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	st, ct := sdk.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func fakeDoc(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "fake.mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func call(t *testing.T, cs *sdk.ClientSession, name string, args any) *sdk.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return res
}

func text(res *sdk.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func structured(t *testing.T, res *sdk.CallToolResult) map[string]any {
	t.Helper()
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("structured content %s: %v", b, err)
	}
	return m
}

func TestServer_listTools(t *testing.T) {
	cs := connect(t, fakeDoc(t))
	list, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range list.Tools {
		names = append(names, tl.Name)
	}
	want := []string{"fake_array", "fake_badout", "fake_echo", "fake_fail", "fake_fail_typed", "fake_flood", "fake_orphan", "fake_plain", "fake_secret", "fake_sleep", "fake_stream"}
	if !slices.Equal(names, want) {
		t.Fatalf("tools = %v, want %v (sorted by name)", names, want)
	}
	if !list.Tools[2].Annotations.ReadOnlyHint {
		t.Error("fake_echo lost its readOnlyHint")
	}
}

func TestServer_echoArgv(t *testing.T) {
	cs := connect(t, fakeDoc(t))
	res := call(t, cs, "fake_echo", map[string]any{
		"words": []any{"-starts-with-dash", "two words"},
		"tag":   []any{"a", "b"},
		"n":     2,
		"on":    false,
		"label": map[string]any{"z": "1", "a": "x=y"},
		"note":  "@literal",
		"stdin": "piped",
	})
	if res.IsError {
		t.Fatalf("error result: %s", text(res))
	}
	got := structured(t, res)
	argv, _ := got["argv"].([]any)
	want := []any{"--json", "--label=a=x=y", "--label=z=1", "-n", "-n", "--note=@@literal", "--no-on", "--tag=a", "--tag=b", "--", "-starts-with-dash", "two words"}
	if fmt.Sprint(argv) != fmt.Sprint(want) {
		t.Errorf("argv = %q\nwant   %q", argv, want)
	}
	if got["stdin"] != "piped" {
		t.Errorf("stdin = %v", got["stdin"])
	}
	if got["env"] != "from-env" {
		t.Errorf("env = %v, want the server's environment", got["env"])
	}
	if res.Meta[metaTrailing] != "done" {
		t.Errorf("trailing stdout = %v, want done", res.Meta[metaTrailing])
	}
	var back map[string]any
	if err := json.Unmarshal([]byte(text(res)), &back); err != nil {
		t.Errorf("text isn't the structured content's JSON: %v", err)
	}
}

func TestServer_nullAndMissing(t *testing.T) {
	cs := connect(t, fakeDoc(t))
	res := call(t, cs, "fake_echo", map[string]any{"note": nil})
	if res.IsError {
		t.Fatalf("error result: %s", text(res))
	}
	argv, _ := structured(t, res)["argv"].([]any)
	if fmt.Sprint(argv) != "[--json]" {
		t.Errorf("argv = %q, want only the fixed flag", argv)
	}
}

func TestServer_countOverDefault(t *testing.T) {
	cs := connect(t, fakeDoc(t))
	res := call(t, cs, "fake_echo", map[string]any{"level": 2, "n": 0})
	if res.IsError {
		t.Fatalf("error result: %s", text(res))
	}
	argv, _ := structured(t, res)["argv"].([]any)
	if fmt.Sprint(argv) != "[--json --level --level]" {
		t.Errorf("argv = %q", argv)
	}
	res = call(t, cs, "fake_echo", map[string]any{"level": 0})
	if !res.IsError || !strings.Contains(text(res), "default") {
		t.Fatalf("want a refusal of 0 over the default, got %s", text(res))
	}
}

func TestServer_badArguments(t *testing.T) {
	cs := connect(t, fakeDoc(t))
	for name, args := range map[string]map[string]any{
		"unknown":  {"bogus": 1},
		"type":     {"n": "three"},
		"negative": {"n": -1},
	} {
		t.Run(name, func(t *testing.T) {
			res := call(t, cs, "fake_echo", args)
			if !res.IsError || !strings.Contains(text(res), "input schema") {
				t.Fatalf("want an input-schema error, got %+v: %s", res.IsError, text(res))
			}
		})
	}
}

func TestServer_secretRefused(t *testing.T) {
	cs := connect(t, fakeDoc(t))
	res := call(t, cs, "fake_secret", map[string]any{"key": "hunter2"})
	if !res.IsError || !strings.Contains(text(res), "secret") {
		t.Fatalf("want a secret refusal, got %s", text(res))
	}
	if strings.Contains(text(res), "hunter2") {
		t.Fatal("the refusal echoes the secret")
	}
}

func TestServer_stream(t *testing.T) {
	cs := connect(t, fakeDoc(t))
	res := call(t, cs, "fake_stream", nil)
	if res.IsError {
		t.Fatalf("error result: %s", text(res))
	}
	b, _ := json.Marshal(res.StructuredContent)
	if string(b) != `[{"n":0},{"n":1},{"n":2}]` {
		t.Errorf("structured = %s", b)
	}
	if res.Meta[metaTrailing] != "streamed 3 items" {
		t.Errorf("trailing = %v", res.Meta[metaTrailing])
	}
}

func TestServer_failure(t *testing.T) {
	cs := connect(t, fakeDoc(t))
	res := call(t, cs, "fake_fail", nil)
	if !res.IsError {
		t.Fatal("want an error result")
	}
	if !strings.Contains(text(res), "Error: boom") {
		t.Errorf("text = %q, want stderr", text(res))
	}
	got := structured(t, res)
	if e, _ := got["error"].(map[string]any); e["message"] != "boom" {
		t.Errorf("structured = %v, want the error line", got)
	}
	if fmt.Sprint(res.Meta[metaExitCode]) != "3" {
		t.Errorf("exit code = %v", res.Meta[metaExitCode])
	}
}

// With an output schema, the error line goes in _meta: clients check structured content against
// the output schema even on an error result.
func TestServer_failureWithOutputSchema(t *testing.T) {
	cs := connect(t, fakeDoc(t))
	res := call(t, cs, "fake_fail_typed", nil)
	if !res.IsError || res.StructuredContent != nil {
		t.Fatalf("want an error result without structured content, got %v %v", res.IsError, res.StructuredContent)
	}
	if !strings.Contains(text(res), "Error: boom") {
		t.Errorf("text = %q, want stderr", text(res))
	}
	line, _ := res.Meta[metaError].(map[string]any)
	if e, _ := line["error"].(map[string]any); e["message"] != "boom" {
		t.Errorf("_meta[%q] = %v, want the error line", metaError, res.Meta[metaError])
	}
}

func TestServer_backgroundChildHoldsOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sleep(1)")
	}
	for _, grace := range []time.Duration{300 * time.Millisecond, 0} {
		t.Run(grace.String(), func(t *testing.T) {
			cs := connect(t, fakeDoc(t), WithGrace(grace))
			res := call(t, cs, "fake_orphan", nil)
			if res.IsError || text(res) != "started\n" || fmt.Sprint(res.Meta[metaExitCode]) != "0" {
				t.Fatalf("got %v %q, exit code %v; want the program's output and exit status", res.IsError, text(res), res.Meta[metaExitCode])
			}
		})
	}
}

func TestServer_outputMismatch(t *testing.T) {
	cs := connect(t, fakeDoc(t))
	res := call(t, cs, "fake_badout", nil)
	if !res.IsError || !strings.Contains(text(res), "output schema") {
		t.Fatalf("want an output-schema error, got %s", text(res))
	}
}

func TestServer_plain(t *testing.T) {
	cs := connect(t, fakeDoc(t))
	res := call(t, cs, "fake_plain", nil)
	if res.IsError || text(res) != "hello\n" || res.StructuredContent != nil {
		t.Fatalf("got %+v %q", res.IsError, text(res))
	}
	if res.Meta[metaStderr] != "a warning" {
		t.Errorf("stderr = %v", res.Meta[metaStderr])
	}
}

func TestServer_outputLimit(t *testing.T) {
	cs := connect(t, fakeDoc(t), WithOutputLimit(64<<10))
	res := call(t, cs, "fake_flood", nil)
	if !res.IsError || !strings.Contains(text(res), "output limit") {
		t.Fatalf("want an output-limit error, got %s", text(res))
	}
}

func TestServer_timeoutSendsSIGTERM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no SIGTERM; the process is killed")
	}
	marker := filepath.Join(t.TempDir(), "signal")
	exe, _ := os.Executable()
	cs := connect(t, fakeDoc(t), WithTimeout(time.Second),
		WithEnv(append(os.Environ(), fakeEnv+"=1", "FAKE_SIGNAL_FILE="+marker)), WithExecutable(exe))
	res := call(t, cs, "fake_sleep", nil)
	if !res.IsError || !strings.Contains(text(res), "timeout") {
		t.Fatalf("want a timeout error, got %s", text(res))
	}
	b, err := os.ReadFile(marker)
	if err != nil || string(b) != "TERM" {
		t.Fatalf("the program didn't see SIGTERM: %q %v", b, err)
	}
}

func TestServer_timeoutWithoutGraceKills(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "signal")
	exe, _ := os.Executable()
	cs := connect(t, fakeDoc(t), WithTimeout(500*time.Millisecond), WithGrace(0),
		WithEnv(append(os.Environ(), fakeEnv+"=1", "FAKE_SIGNAL_FILE="+marker)), WithExecutable(exe))
	start := time.Now()
	res := call(t, cs, "fake_sleep", nil)
	if !res.IsError || !strings.Contains(text(res), "timeout") {
		t.Fatalf("want a timeout error, got %s", text(res))
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("the run took %s to stop", took)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the program got SIGTERM; with no grace period it is killed")
	}
}

func TestServer_cancelSendsSIGTERM(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no SIGTERM; the process is killed")
	}
	marker := filepath.Join(t.TempDir(), "signal")
	exe, _ := os.Executable()
	cs := connect(t, fakeDoc(t), WithEnv(append(os.Environ(), fakeEnv+"=1", "FAKE_SIGNAL_FILE="+marker)), WithExecutable(exe))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "fake_sleep"})
		done <- err
	}()
	time.Sleep(500 * time.Millisecond)
	cancel()
	<-done
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(marker); err == nil && string(b) == "TERM" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the program didn't see SIGTERM after the call was canceled")
}

func TestServer_revision20251125(t *testing.T) {
	cs := connect(t, fakeDoc(t), WithRevision(Revision20251125))
	list, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range list.Tools {
		if tl.Name != "fake_array" {
			continue
		}
		b, _ := json.Marshal(tl.OutputSchema)
		if !strings.Contains(string(b), `"result"`) || !strings.Contains(string(b), `"type":"object"`) {
			t.Errorf("fake_array's output schema isn't wrapped: %s", b)
		}
	}
	res := call(t, cs, "fake_array", nil)
	b, _ := json.Marshal(res.StructuredContent)
	if res.IsError || string(b) != `{"result":[1,2]}` {
		t.Fatalf("got %v %s: %s", res.IsError, b, text(res))
	}
}

func TestServer_selectors(t *testing.T) {
	cs := connect(t, fakeDoc(t),
		WithSelector(func(tl Tool) bool { return tl.Name == "fake_echo" }),
		WithParameterSelector(func(_ Tool, p string) bool { return p != "label" }))
	list, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(list.Tools))
	}
	b, _ := json.Marshal(list.Tools[0].InputSchema)
	if strings.Contains(string(b), "label") {
		t.Errorf("label is still a parameter: %s", b)
	}
	res := call(t, cs, "fake_echo", map[string]any{"label": map[string]any{"a": "b"}})
	if !res.IsError {
		t.Error("a left-out parameter was accepted")
	}
}

func TestServer_middleware(t *testing.T) {
	var seen []string
	mw := func(next Handler) Handler {
		return func(ctx context.Context, c *Call) (*sdk.CallToolResult, error) {
			seen = append(seen, strings.Join(c.Argv, " "))
			c.Argv = append(c.Argv, "added")
			return next(ctx, c)
		}
	}
	cs := connect(t, fakeDoc(t), WithMiddleware(mw))
	res := call(t, cs, "fake_echo", map[string]any{"words": []any{"w"}})
	argv, _ := structured(t, res)["argv"].([]any)
	if fmt.Sprint(argv) != "[--json -- w added]" || len(seen) != 1 || seen[0] != "echo --json -- w" {
		t.Fatalf("argv = %q, seen = %q", argv, seen)
	}
}

func TestNewServer_errors(t *testing.T) {
	doc := fakeDoc(t)
	cases := map[string]struct {
		doc  string
		opts []Option
		want string
	}{
		"not json":     {doc: "{", want: "isn't JSON"},
		"no tools":     {doc: "{}", want: `no "tools"`},
		"no meta":      {doc: `{"tools":[{"name":"x","inputSchema":{"type":"object"}}]}`, want: "_meta"},
		"input schema": {doc: `{"tools":[{"name":"x","inputSchema":{"type":"string"},"_meta":{"dev.rotini/invoke":{"path":[]}}}]}`, want: "inputSchema"},
		"duplicate":    {doc: `{"tools":[{"name":"x","inputSchema":{"type":"object"},"_meta":{"dev.rotini/invoke":{"path":[]}}},{"name":"x","inputSchema":{"type":"object"},"_meta":{"dev.rotini/invoke":{"path":[]}}}]}`, want: "two tools"},
		"kind":         {doc: `{"tools":[{"name":"x","inputSchema":{"type":"object"},"_meta":{"dev.rotini/invoke":{"path":[],"flags":{"a":{"flag":"--a","kind":"weird"}}}}}]}`, want: "unknown kind"},
		"revision":     {doc: string(doc), opts: []Option{WithRevision("2024-01-01")}, want: "unknown MCP revision"},
		"serving":      {doc: string(doc), opts: []Option{WithServingCommand("echo")}, want: "serving command"},
		"required":     {doc: `{"tools":[{"name":"x","inputSchema":{"type":"object","properties":{"a":{}},"required":["a"]},"_meta":{"dev.rotini/invoke":{"path":[]}}}]}`, opts: []Option{WithParameterSelector(func(Tool, string) bool { return false })}, want: "required"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewServer([]byte(tc.doc), append([]Option{WithExecutable("x")}, tc.opts...)...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestServe_stdio(t *testing.T) {
	exe, _ := os.Executable()
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, fakeDoc(t), WithStdio(sr, sw), WithExecutable(exe),
			WithEnv(append(os.Environ(), fakeEnv+"=1")), WithImplementation("fake", "1.0.0"))
	}()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil).
		Connect(ctx, &sdk.IOTransport{Reader: cr, Writer: cw}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := cs.InitializeResult().ServerInfo.Name; got != "fake" {
		t.Errorf("server name = %q", got)
	}
	res := call(t, cs, "fake_plain", nil)
	if text(res) != "hello\n" {
		t.Errorf("text = %q", text(res))
	}
	cancel()
	_ = cs.Close()
	if err := <-done; err != nil {
		t.Errorf("Serve returned %v after its context was canceled", err)
	}
}
