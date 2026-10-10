package rotinimcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-rotini/rotini"

	"github.com/go-rotini/mcp"
)

var _ rotini.Handler = (*rotiniMcpServeHandler)(nil)

type rotiniMcpServeHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniMcpServeHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniMcpServeInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	f := inputs.RotiniMcpServe.Flags

	doc, err := os.ReadFile(underDir(rtx.Dir(), f.Tools))
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	bin, err := program(rtx.Dir(), f.Bin)
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	if err := checkEnv(f.Env); err != nil {
		rtx.HaltWith(err)
		return
	}
	if err := knownTools(doc, f.Tool); err != nil {
		rtx.HaltWith(err)
		return
	}

	opts := []mcp.Option{
		mcp.WithExecutable(bin),
		mcp.WithStdio(rtx.Stdin, rtx.Stdout),
		mcp.WithEnv(append(rtx.Environ(), f.Env...)),
		mcp.WithDir(rtx.Dir()),
		mcp.WithTimeout(f.Timeout),
		mcp.WithGrace(f.Grace),
		mcp.WithOutputLimit(int64(f.OutputLimit)),
		mcp.WithRevision(f.Revision),
		mcp.WithLog(rtx.Stderr),
	}
	if len(f.Tool) > 0 {
		opts = append(opts, mcp.WithSelector(func(t mcp.Tool) bool { return slices.Contains(f.Tool, t.Name) }))
	}
	if err := mcp.Serve(ctx, doc, opts...); err != nil {
		rtx.HaltWith(err)
	}
}

// underDir resolves a path typed relative to the run's directory.
func underDir(dir, path string) string {
	if filepath.IsAbs(path) || dir == "" {
		return path
	}
	return filepath.Join(dir, path)
}

// program resolves the program to run: a path (anything with a separator) against the run's
// directory, a bare name on PATH.
func program(dir, bin string) (string, error) {
	if strings.ContainsRune(bin, '/') || strings.ContainsRune(bin, filepath.Separator) {
		abs, err := filepath.Abs(underDir(dir, bin))
		if err != nil {
			return "", fmt.Errorf("resolve --bin: %w", err)
		}
		return abs, nil
	}
	path, err := exec.LookPath(bin)
	if err != nil {
		return "", fmt.Errorf("--bin %s: %w", bin, err)
	}
	return path, nil
}

// knownTools refuses a --tool the tools file doesn't have.
func knownTools(doc []byte, names []string) error {
	if len(names) == 0 {
		return nil
	}
	tools, err := mcp.ParseTools(doc)
	if err != nil {
		return err
	}
	for _, name := range names {
		if !slices.ContainsFunc(tools, func(t mcp.Tool) bool { return t.Name == name }) {
			return fmt.Errorf("--tool %s: the tools file has no such tool", name)
		}
	}
	return nil
}
