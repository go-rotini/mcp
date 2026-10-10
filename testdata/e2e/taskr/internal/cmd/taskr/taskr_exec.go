package taskr

import (
	"context"
	"encoding/json"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*taskrExecHandler)(nil)

type taskrExecHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*taskrExecHandler) Run(ctx context.Context, rtx *rotini.Context) {
	in, err := rtx.Inputs[TaskrExecInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	out := map[string]any{
		"argv":    append([]string{}, in.TaskrExec.Arguments.Argv...),
		"json":    in.Taskr.Flags.JSON,
		"verbose": in.Taskr.Flags.Verbose,
	}
	if err := json.NewEncoder(rtx.Stdout).Encode(out); err != nil {
		rtx.HaltWith(err)
	}
}
