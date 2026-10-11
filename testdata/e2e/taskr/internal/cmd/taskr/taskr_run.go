package taskr

import (
	"context"
	"encoding/json"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*taskrRunHandler)(nil)

type taskrRunHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*taskrRunHandler) Run(ctx context.Context, rtx *rotini.Context) {
	in, err := rtx.Inputs[TaskrRunInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	a := in.TaskrRun.Arguments
	out := map[string]any{"script": a.Script, "args": append([]string{}, a.Args...)}
	if err := json.NewEncoder(rtx.Stdout).Encode(out); err != nil {
		rtx.HaltWith(err)
	}
}
