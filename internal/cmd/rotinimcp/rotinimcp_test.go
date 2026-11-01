package rotinimcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func runCLI(t *testing.T, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code, _ = NewProgram().
		WithDir(dir).
		WithStdout(&out).
		WithStderr(&errOut).
		WithExit(func(int) {}).
		Run(args)
	return code, out.String(), errOut.String()
}

// isolate points the user's files (home and config directory) at a temporary directory, so a
// test never reads or writes the real ones.
func isolate(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APPDATA", filepath.Join(home, "AppData"))
}

// A program serving itself: the client runs the program with the arguments after it.
func TestAdd_selfServing(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	code, out, errOut := runCLI(t, dir, "add", "claude-code", "taskr", "mcp", "--env", "TASKR_TOKEN=x=y")
	if code != 0 || !strings.Contains(out, "added taskr to") {
		t.Fatalf("add: %d %q %q", code, out, errOut)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	s := doc.MCPServers["taskr"]
	if s.Command != "taskr" || strings.Join(s.Args, " ") != "mcp" || s.Env["TASKR_TOKEN"] != "x=y" {
		t.Fatalf("entry = %+v", s)
	}
	code, out, _ = runCLI(t, dir, "list", "claude-code")
	if want := []string{"claude-code", "project", "taskr", "taskr", "mcp"}; code != 0 || !slices.Equal(strings.Fields(out), want) {
		t.Fatalf("list: %d %q", code, out)
	}
	code, out, _ = runCLI(t, dir, "remove", "claude-code", "taskr")
	if code != 0 || !strings.Contains(out, "removed taskr") {
		t.Fatalf("remove: %d %q", code, out)
	}
	code, out, _ = runCLI(t, dir, "remove", "claude-code", "taskr")
	if code != 0 || !strings.Contains(out, "no server named taskr") {
		t.Fatalf("remove again: %d %q", code, out)
	}
}

// With only the program, the client runs rotini-mcp serve; the tools file defaults to
// tools/mcp.json and the server is named after the program.
func TestAdd_served(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"tools/mcp.json", "taskr"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, _, errOut := runCLI(t, dir, "add", "cursor", "./taskr", "--scope", "project")
	if code != 0 {
		t.Fatalf("add: %d %q", code, errOut)
	}
	code, out, _ := runCLI(t, dir, "list", "--scope", "project")
	want := " serve " + filepath.Join(dir, "taskr") + " --tools " + filepath.Join(dir, "tools", "mcp.json") + "\n"
	if code != 0 || !strings.HasPrefix(out, "cursor") || !strings.Contains(out, " taskr ") || !strings.HasSuffix(out, want) {
		t.Fatalf("list: %d %q, want it to end %q", code, out, want)
	}
	code, _, _ = runCLI(t, dir, "add", "cursor", "./taskr", "--scope", "project", "--name", "dev")
	if code != 0 {
		t.Fatal("add --name failed")
	}
	code, out, _ = runCLI(t, dir, "remove", "cursor", "./taskr", "--scope", "project")
	if code != 0 || !strings.Contains(out, "removed taskr") {
		t.Fatalf("remove by program: %d %q", code, out)
	}
	code, out, _ = runCLI(t, dir, "list", "cursor", "--scope", "project")
	if code != 0 || !strings.Contains(out, " dev ") || strings.Contains(out, " taskr ") {
		t.Fatalf("list after remove: %d %q", code, out)
	}
}

// --permissions turns the tools' annotations into Claude Code rules, and remove takes them away.
func TestAdd_permissions(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	tools := `{"tools":[
  {"name":"notes_list","inputSchema":{"type":"object"},"annotations":{"readOnlyHint":true},"_meta":{"dev.rotini/invoke":{"path":["list"]}}},
  {"name":"notes_add","inputSchema":{"type":"object"},"annotations":{"readOnlyHint":false,"destructiveHint":false},"_meta":{"dev.rotini/invoke":{"path":["add"]}}},
  {"name":"notes_clear","inputSchema":{"type":"object"},"annotations":{"destructiveHint":true},"_meta":{"dev.rotini/invoke":{"path":["clear"]}}},
  {"name":"notes_misc","inputSchema":{"type":"object"},"_meta":{"dev.rotini/invoke":{"path":["misc"]}}}
]}`
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(tools), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runCLI(t, dir, "add", "claude-code", "./notes", "mcp", "--tools", "mcp.json", "--permissions")
	if code != 0 || !strings.Contains(out, "1 allowed, 1 asked") {
		t.Fatalf("add: %d %q %q", code, out, errOut)
	}
	settings := filepath.Join(dir, ".claude", "settings.json")
	var got struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Ask   []string `json:"ask"`
		} `json:"permissions"`
	}
	b, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Permissions.Allow, ",") != "mcp__notes__notes_list" || strings.Join(got.Permissions.Ask, ",") != "mcp__notes__notes_clear" {
		t.Errorf("rules = %+v", got.Permissions)
	}

	code, out, _ = runCLI(t, dir, "remove", "claude-code", "notes")
	if code != 0 || !strings.Contains(out, "removed the tool rules for notes") {
		t.Fatalf("remove: %d %q", code, out)
	}
	if b, _ := os.ReadFile(settings); strings.Contains(string(b), "mcp__notes__") || strings.Contains(string(b), "permissions") {
		t.Errorf("rules left after remove:\n%s", b)
	}
}

// A bare program is a name on PATH, as for serve, and is written as the path it resolves to.
func TestAdd_programOnPath(t *testing.T) {
	isolate(t)
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go on PATH")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runCLI(t, dir, "add", "claude-code", "go", "--tools", "mcp.json")
	if code != 0 {
		t.Fatalf("add: %d %q", code, errOut)
	}
	code, out, _ := runCLI(t, dir, "list", "claude-code")
	if want := " serve " + goBin + " --tools "; code != 0 || !strings.Contains(out, want) {
		t.Fatalf("list: %d %q, want it to contain %q", code, out, want)
	}
	code, _, errOut = runCLI(t, dir, "add", "claude-code", "no-such-program-on-path", "--tools", "mcp.json")
	if code == 0 || !strings.Contains(errOut, "no-such-program-on-path") {
		t.Fatalf("a missing program: %d %q", code, errOut)
	}
}

// A missing tools file says where one comes from.
func TestAdd_noToolsFile(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runCLI(t, dir, "add", "claude-code", "./notes")
	if code == 0 || !strings.Contains(errOut, "tools feature") {
		t.Fatalf("%d %q", code, errOut)
	}
}

// A rotini-mcp in Go's build cache is written as `go -C <module> tool rotini-mcp`, since the
// cache can be cleaned.
func TestUnder(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		path, dir string
		want      bool
	}{
		{filepath.Join(root, "cache", "ab", "x-d", "rotini-mcp"), filepath.Join(root, "cache"), true},
		{filepath.Join(root, "bin", "rotini-mcp"), filepath.Join(root, "cache"), false},
		{filepath.Join(root, "cache-other", "rotini-mcp"), filepath.Join(root, "cache"), false},
		{filepath.Join(root, "rotini-mcp"), "", false},
	} {
		if got := under(tc.path, tc.dir); got != tc.want {
			t.Errorf("under(%q, %q) = %v, want %v", tc.path, tc.dir, got, tc.want)
		}
	}
}

func TestServerName(t *testing.T) {
	for in, want := range map[string]string{"./bin/notes": "notes", "notes": "notes", `C:\bin\notes.exe`: "notes", "notes.exe": "notes"} {
		if runtime.GOOS != "windows" && strings.Contains(in, `\`) {
			continue
		}
		if got := serverName(in); got != want {
			t.Errorf("serverName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRefuseBatchFile(t *testing.T) {
	for _, tc := range []struct {
		goos, path string
		refused    bool
	}{
		{"windows", `C:\tools\taskr.bat`, true},
		{"windows", `C:\tools\taskr.CMD`, true},
		{"windows", `C:\tools\taskr.exe`, false},
		{"linux", "/usr/bin/taskr.bat", false},
		{"darwin", "/usr/local/bin/taskr", false},
	} {
		err := refuseBatchFile(tc.goos, tc.path)
		if (err != nil) != tc.refused {
			t.Errorf("%s %s: %v, want refused %v", tc.goos, tc.path, err, tc.refused)
		}
		if err != nil && !strings.Contains(err.Error(), "cmd.exe") {
			t.Errorf("%s: the error doesn't say why: %v", tc.path, err)
		}
	}
}

func TestCLI_errors(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	for name, args := range map[string][]string{
		"no program":         {"add", "claude-code"},
		"bad scope":          {"add", "claude-desktop", "c", "mcp", "--scope", "project"},
		"client":             {"list", "emacs"},
		"env":                {"add", "claude-code", "c", "mcp", "--env", "NOEQUALS"},
		"permissions client": {"add", "cursor", "c", "mcp", "--tools", "go.mod", "--permissions"},
		"permissions tools":  {"add", "claude-code", "c", "mcp", "--permissions"},
		"serve no program":   {"serve"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if code, _, _ := runCLI(t, dir, args...); code == 0 {
				t.Fatal("succeeded")
			}
		})
	}
}

func TestServe_unknownTool(t *testing.T) {
	code, _, errOut := runCLI(t, "../../..", "serve", "go", "--tools", "testdata/fake.mcp.json", "--tool", "nope")
	if code == 0 || !strings.Contains(errOut, "no such tool") {
		t.Fatalf("%d %q", code, errOut)
	}
}

func TestServe_stdio(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses echo as the program")
	}
	echo, err := exec.LookPath("echo")
	if err != nil {
		t.Skip("no echo on PATH")
	}
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan int, 1)
	go func() {
		code, _ := NewProgram().
			WithDir("../../..").
			WithStdin(sr).
			WithStdout(sw).
			WithStderr(io.Discard).
			WithContext(ctx).
			WithExit(func(int) {}).
			Run([]string{"serve", echo, "--tools", "testdata/fake.mcp.json", "--tool", "fake_plain"})
		done <- code
	}()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil).
		Connect(ctx, &sdk.IOTransport{Reader: cr, Writer: cw}, nil)
	if err != nil {
		t.Fatal(err)
	}
	list, err := cs.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 1 {
		t.Fatalf("tools: %v %v", list, err)
	}
	res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "fake_plain"})
	if err != nil {
		t.Fatal(err)
	}
	if tc, ok := res.Content[0].(*sdk.TextContent); !ok || tc.Text != "plain\n" {
		t.Fatalf("result = %+v", res.Content)
	}
	_ = cs.Close()
	cancel()
	if code := <-done; code != 0 {
		t.Errorf("serve exited %d", code)
	}
}

// A program path is made absolute and must exist; a project file that ends up with this
// machine's paths gets a note.
func TestAdd_paths(t *testing.T) {
	isolate(t)
	dir := t.TempDir()
	for _, f := range []string{"mcp.json", "taskr"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, _, errOut := runCLI(t, dir, "add", "claude-code", "./taskr", "mcp")
	if code != 0 || !strings.Contains(errOut, "paths on this machine") {
		t.Fatalf("add: %d %q", code, errOut)
	}
	code, out, _ := runCLI(t, dir, "list", "claude-code")
	if want := "taskr  " + filepath.Join(dir, "taskr") + " mcp\n"; code != 0 || !strings.HasSuffix(out, want) {
		t.Fatalf("list: %d %q, want it to end %q", code, out, want)
	}
	code, _, errOut = runCLI(t, dir, "add", "claude-code", "taskr", "mcp", "--name", "bare")
	if code != 0 || strings.Contains(errOut, "paths on this machine") {
		t.Fatalf("a bare program: %d %q", code, errOut)
	}
	for name, args := range map[string][]string{
		"self":       {"./nope", "mcp"},
		"served":     {"./nope", "--tools", "mcp.json"},
		"served dir": {".", "--tools", "mcp.json"},
	} {
		code, _, errOut := runCLI(t, dir, append([]string{"add", "claude-code"}, args...)...)
		if code == 0 || !strings.Contains(errOut, "nope") && name != "served dir" {
			t.Errorf("%s: %d %q, want a refusal", name, code, errOut)
		}
	}
}
