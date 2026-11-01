package taskr

import (
	"context"
	"encoding/json"

	"github.com/go-rotini/rotini"
)

var _ rotini.HandlerE = (*taskrExecHandler)(nil)

type taskrExecHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*taskrExecHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	in, err := rtx.Inputs[TaskrExecInputs]()
	if err != nil {
		return err
	}
	out := map[string]any{
		"argv":    append([]string{}, in.TaskrExec.Arguments.Argv...),
		"json":    in.Taskr.Flags.JSON,
		"verbose": in.Taskr.Flags.Verbose,
	}
	return json.NewEncoder(rtx.Stdout).Encode(out)
}
