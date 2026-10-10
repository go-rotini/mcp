package rotinimcp

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"

	"github.com/go-rotini/mcp/clients"
)

var _ rotini.Handler = (*rotiniMcpConfigDisableHandler)(nil)

type rotiniMcpConfigDisableHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniMcpConfigDisableHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniMcpConfigDisableInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	f, a := inputs.RotiniMcpConfigDisable.Flags, inputs.RotiniMcpConfigDisable.Arguments

	path, removed, err := clients.Disable(clients.Client(a.Client), clients.Scope(f.Scope), clients.Dirs{Project: rtx.Dir()}, a.Name)
	if err != nil {
		rtx.HaltWith(err)
		return
	}
	msg := fmt.Sprintf("removed %s from %s", a.Name, path)
	if !removed {
		msg = fmt.Sprintf("%s has no server named %s", path, a.Name)
	}
	if _, err := fmt.Fprintln(rtx.Stdout, msg); err != nil {
		rtx.HaltWith(err)
	}
}
