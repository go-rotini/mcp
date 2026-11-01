package taskr

import (
	"context"
	"encoding/json"

	"github.com/go-rotini/rotini"
)

var _ rotini.HandlerE = (*taskrRunHandler)(nil)

type taskrRunHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*taskrRunHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	in, err := rtx.Inputs[TaskrRunInputs]()
	if err != nil {
		return err
	}
	a := in.TaskrRun.Arguments
	out := map[string]any{"script": a.Script, "args": append([]string{}, a.Args...)}
	return json.NewEncoder(rtx.Stdout).Encode(out)
}
