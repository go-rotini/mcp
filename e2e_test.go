package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestEndToEnd builds a rotini program from the spec in testdata/e2e/taskr with the rotini this
// module builds against, generates its tools/mcp.json (and ToolsMCP), and serves it both ways:
// with rotini-mcp serve, and from the program's own mcp command. Each is started over stdio,
// lists its tools, and runs calls through rotini's real parser.
func TestEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds programs; skipped with -short")
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	rotiniDir, replaced := rotiniModule(t, root)

	rotiniBin := filepath.Join(bin, "rotini"+exeSuffix())
	gocmd(t, rotiniDir, []string{"GOWORK=off"}, "build", "-o", rotiniBin, "./cmd/rotini")
	serverBin := filepath.Join(bin, "rotini-mcp"+exeSuffix())
	gocmd(t, root, nil, "build", "-o", serverBin, "./cmd/rotini-mcp")

	app := filepath.Join(tmp, "taskr")
	copyDir(t, filepath.Join("testdata", "e2e", "taskr"), app)
	gomod := "module example.com/taskr\n\ngo 1.27\n\n" +
		"require (\n\tgithub.com/go-rotini/mcp v0.0.0\n\tgithub.com/go-rotini/rotini " + rotiniVersion(t, root) + "\n)\n\n" +
		"replace github.com/go-rotini/mcp => " + root + "\n"
	if replaced {
		gomod += "\nreplace github.com/go-rotini/rotini => " + rotiniDir + "\n"
	}
	if err := os.WriteFile(filepath.Join(app, "go.mod"), []byte(gomod), 0o600); err != nil {
		t.Fatal(err)
	}
	runCmd(t, app, nil, rotiniBin, "generate", ".rotini.spec.yaml", "--config", ".rotini.conf.yaml")
	toolsFile := filepath.Join(app, "tools", "mcp.json")
	if _, err := os.Stat(toolsFile); err != nil {
		t.Fatalf("rotini generate wrote no tools/mcp.json: %v", err)
	}
	gocmd(t, app, []string{"GOWORK=off", "GOFLAGS=-mod=mod"}, "mod", "tidy")
	taskrBin := filepath.Join(bin, "taskr"+exeSuffix())
	gocmd(t, app, []string{"GOWORK=off"}, "build", "-o", taskrBin, "./cmd/taskr")

	forms := map[string]*exec.Cmd{
		"rotini-mcp serve": exec.Command(serverBin, "serve", "--tools", toolsFile, "--bin", taskrBin),
		"taskr mcp":        exec.Command(taskrBin, "mcp"),
	}
	for name, cmd := range forms {
		t.Run(name, func(t *testing.T) {
			cmd.Env = append(os.Environ(), "TASKR_JSON_ERRORS=1")
			cmd.Stderr = &bytes.Buffer{}
			cs, err := sdk.NewClient(&sdk.Implementation{Name: "e2e", Version: "0"}, nil).
				Connect(t.Context(), &sdk.CommandTransport{Command: cmd}, nil)
			if err != nil {
				t.Fatalf("connect: %v; server stderr:\n%s", err, cmd.Stderr)
			}
			defer cs.Close()
			checkEndToEnd(t, cs)
		})
	}
}

func checkEndToEnd(t *testing.T, cs *sdk.ClientSession) {
	t.Helper()
	list, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range list.Tools {
		names = append(names, tl.Name)
	}
	if !slices.Equal(names, []string{"taskr_add", "taskr_fail"}) {
		t.Fatalf("tools = %v; the mcp command must not be one", names)
	}

	res := call(t, cs, "taskr_add", map[string]any{
		"title":    "-starts with a dash",
		"tag":      []any{"home", "a b"},
		"priority": "high",
		"done":     false,
		"label":    map[string]any{"team": "core", "k": "x=y"},
		"note":     "--not-a-flag",
		"verbose":  2,
	})
	if res.IsError {
		t.Fatalf("taskr_add: %s", text(res))
	}
	got, _ := json.Marshal(res.StructuredContent)
	want := `{"done":false,"label":{"k":"x=y","team":"core"},"note":"--not-a-flag","priority":"high","tags":["home","a b"],"title":"-starts with a dash","verbose":2}`
	if string(got) != want {
		t.Errorf("taskr_add bound\n%s\nwant\n%s", got, want)
	}

	res = call(t, cs, "taskr_add", map[string]any{"title": "defaults", "tag": nil})
	got, _ = json.Marshal(res.StructuredContent)
	if res.IsError || !strings.Contains(string(got), `"done":true`) || !strings.Contains(string(got), `"priority":"low"`) {
		t.Errorf("defaults: %s %s", got, text(res))
	}

	res = call(t, cs, "taskr_add", map[string]any{"priority": "urgent"})
	if !res.IsError {
		t.Error("bad arguments reached the program")
	}

	res = call(t, cs, "taskr_fail", nil)
	if !res.IsError {
		t.Fatal("taskr_fail succeeded")
	}
	line := structured(t, res)
	e, _ := line["error"].(map[string]any)
	if fmt.Sprint(line["schema_version"]) != "1" || !strings.Contains(fmt.Sprint(e["message"]), "the list is locked") {
		t.Errorf("structured error = %v", line)
	}
}

// rotiniModule finds the rotini module this one builds against, and whether it is a local
// replacement (a scratch go.work before rotini's release) rather than a published version.
func rotiniModule(t *testing.T, root string) (dir string, replaced bool) {
	t.Helper()
	out := output(t, root, nil, "go", "list", "-m", "-json", "github.com/go-rotini/rotini")
	var m struct {
		Dir     string `json:"Dir"`
		Replace *struct {
			Dir string `json:"Dir"`
		} `json:"Replace"`
	}
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m.Replace != nil && m.Replace.Dir != "" {
		return m.Replace.Dir, true
	}
	if m.Dir == "" {
		t.Fatal("go list found no directory for github.com/go-rotini/rotini")
	}
	return m.Dir, false
}

// rotiniVersion is the rotini version go.mod requires.
func rotiniVersion(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(b)) {
		if f := strings.Fields(line); len(f) == 2 && f[0] == "github.com/go-rotini/rotini" {
			return f[1]
		}
	}
	t.Fatal("go.mod doesn't require github.com/go-rotini/rotini")
	return ""
}

func gocmd(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	runCmd(t, dir, env, "go", args...)
}

func runCmd(t *testing.T, dir string, env []string, name string, args ...string) {
	t.Helper()
	output(t, dir, env, name, args...)
}

func output(t *testing.T, dir string, env []string, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %s (in %s): %v\n%s", name, strings.Join(args, " "), dir, err, stderr.String())
	}
	return out
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}
