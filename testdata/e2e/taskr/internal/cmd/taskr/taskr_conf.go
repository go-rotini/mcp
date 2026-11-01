package taskr

import (
	"context"
	"encoding/json"

	"github.com/go-rotini/rotini"
)

var _ rotini.HandlerE = (*taskrConfHandler)(nil)

type taskrConfHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*taskrConfHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	in, err := rtx.Inputs[TaskrConfInputs]()
	if err != nil {
		return err
	}
	set := in.TaskrConf.Flags.Set
	if set == nil {
		set = map[string]any{}
	}
	out := map[string]any{"keys": append([]string{}, in.TaskrConf.Arguments.Keys...), "set": set}
	return json.NewEncoder(rtx.Stdout).Encode(out)
}
