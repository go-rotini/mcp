package rotinimcp

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/go-rotini/rotini"

	"github.com/go-rotini/mcp"
)

var _ rotini.HandlerE = (*rotiniMcpServeHandler)(nil)

type rotiniMcpServeHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniMcpServeHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniMcpServeInputs]()
	if err != nil {
		return err
	}
	f, a := inputs.RotiniMcpServe.Flags, inputs.RotiniMcpServe.Arguments

	doc, err := readTools(rtx.Dir(), f.Tools)
	if err != nil {
		return err
	}
	if err := checkEnv(f.Env); err != nil {
		return err
	}
	if err := knownTools(doc, f.Tool); err != nil {
		return err
	}
	var bin string
	if a.Program == "" {
		var remove func()
		if bin, remove, err = buildProgram(ctx, rtx.Dir(), doc); err != nil {
			return err
		}
		defer remove()
	} else if bin, err = program(rtx.Dir(), a.Program); err != nil {
		return err
	}

	opts := []mcp.Option{
		mcp.WithExecutable(bin),
		mcp.WithStdio(rtx.Stdin, rtx.Stdout),
		mcp.WithEnv(append(rtx.Environ(), f.Env...)),
		mcp.WithDir(rtx.Dir()),
		mcp.WithTimeout(f.Timeout),
		mcp.WithGrace(f.Grace),
		mcp.WithOutputLimit(int64(f.OutputLimit)),
		mcp.WithMaxConcurrent(f.Concurrency),
		mcp.WithRevision(f.Revision),
		mcp.WithSuccessStderr(successStderr(f.SuccessStderr)),
		mcp.WithLog(rtx.Stderr),
	}
	if len(f.Tool) > 0 {
		opts = append(opts, mcp.WithSelector(func(t mcp.Tool) bool { return slices.Contains(f.Tool, t.Name) }))
	}
	return mcp.Serve(ctx, doc, opts...)
}

// successStderr is the mode a --success-stderr value names.
func successStderr(v string) mcp.SuccessStderr {
	if v == RotiniMcpServeSuccessStderrMeta {
		return mcp.SuccessStderrMeta
	}
	return mcp.SuccessStderrContent
}

// readTools reads the tools file, explaining where one comes from when it's missing.
func readTools(dir, path string) ([]byte, error) {
	doc, err := os.ReadFile(underDir(dir, path))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no tools file at %s: turn on the tools feature in the program's conf and run go generate, or give its path with --tools", path)
	}
	return doc, err
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
	var path string
	if strings.ContainsRune(bin, '/') || strings.ContainsRune(bin, filepath.Separator) {
		abs, err := filepath.Abs(underDir(dir, bin))
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", bin, err)
		}
		if err := isFile(abs); err != nil {
			return "", fmt.Errorf("%s: %w", bin, err)
		}
		path = abs
	} else {
		found, err := exec.LookPath(bin)
		if err != nil {
			return "", fmt.Errorf("%s: %w", bin, err)
		}
		path = found
	}
	if err := refuseBatchFile(runtime.GOOS, path); err != nil {
		return "", err
	}
	return path, nil
}

// refuseBatchFile refuses a Windows batch file: Windows runs it through cmd.exe, which parses
// its arguments again, so a call's values could run as commands.
func refuseBatchFile(goos, path string) error {
	if goos != "windows" {
		return nil
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".bat", ".cmd":
		return fmt.Errorf("%s: a batch file runs through cmd.exe, which parses its arguments again, so a call's values could run as commands; give the program itself", path)
	}
	return nil
}

// isFile checks that path names a file that exists.
func isFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New("it is a directory")
	}
	return nil
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
