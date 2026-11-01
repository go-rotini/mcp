package rotinimcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// toolsProgram is what a tools document says about its program: its name, and its main package
// relative to the module root.
type toolsProgram struct {
	Name string `json:"name"`
	Main string `json:"main"`
}

// programOf reads the program a tools document names, for building it from source.
func programOf(doc []byte) (toolsProgram, error) {
	var file struct {
		Meta struct {
			Program toolsProgram `json:"dev.rotini/program"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(doc, &file); err != nil {
		return toolsProgram{}, fmt.Errorf("the tools file isn't JSON: %w", err)
	}
	p := file.Meta.Program
	switch {
	case p.Name == "":
		return toolsProgram{}, errors.New("the tools file doesn't name its program, so it can't be built from source: " +
			"regenerate it with a newer rotini, or give the program to run")
	case p.Main == "":
		return toolsProgram{}, fmt.Errorf("the tools file doesn't name %s's main package, so it can't be built from source: "+
			"declare a main package in the conf, or give the program to run", p.Name)
	}
	return p, nil
}

// buildProgram builds the program a tools document names, from the module dir is in, into a
// new temporary directory. remove deletes the build.
func buildProgram(ctx context.Context, dir string, doc []byte) (bin string, remove func(), err error) {
	p, err := programOf(doc)
	if err != nil {
		return "", nil, err
	}
	_, gomod := goEnv(ctx, dir)
	if gomod == "" || gomod == os.DevNull {
		return "", nil, fmt.Errorf("%s isn't in a Go module, so %s can't be built from source; give the program to run", dir, p.Name)
	}
	tmp, err := os.MkdirTemp("", "rotini-mcp-")
	if err != nil {
		return "", nil, err
	}
	remove = func() { _ = os.RemoveAll(tmp) }
	bin = filepath.Join(tmp, p.Name)
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.CommandContext(ctx, "go", "build", "-o", bin, p.Main)
	cmd.Dir = filepath.Dir(gomod)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		remove()
		return "", nil, fmt.Errorf("build %s from %s: %w\n%s", p.Name, p.Main, err, strings.TrimSpace(stderr.String()))
	}
	return bin, remove, nil
}
