package taskr

import (
	"context"
	"encoding/json"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*taskrConfHandler)(nil)

type taskrConfHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*taskrConfHandler) Run(ctx context.Context, rtx *rotini.Context) {
	in, err := rtx.Inputs[TaskrConfInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	set := in.TaskrConf.Flags.Set
	if set == nil {
		set = map[string]any{}
	}
	out := map[string]any{"keys": append([]string{}, in.TaskrConf.Arguments.Keys...), "set": set}
	if err := json.NewEncoder(rtx.Stdout).Encode(out); err != nil {
		rtx.HaltWith(err)
	}
}
