package rotinimcp

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"

	"github.com/go-rotini/mcp/clients"
)

var _ rotini.HandlerE = (*rotiniMcpConfigDisableHandler)(nil)

type rotiniMcpConfigDisableHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniMcpConfigDisableHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniMcpConfigDisableInputs]()
	if err != nil {
		return err
	}
	f, a := inputs.RotiniMcpConfigDisable.Flags, inputs.RotiniMcpConfigDisable.Arguments

	path, removed, err := clients.Disable(clients.Client(a.Client), clients.Scope(f.Scope), clients.Dirs{Project: rtx.Dir()}, a.Name)
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("removed %s from %s", a.Name, path)
	if !removed {
		msg = fmt.Sprintf("%s has no server named %s", path, a.Name)
	}
	if _, err := fmt.Fprintln(rtx.Stdout, msg); err != nil {
		return err
	}
	if clients.Client(a.Client) != clients.ClaudeCode {
		return nil
	}
	path, removed, err = clients.ClearPermissions(clients.ClaudeCode, clients.Dirs{Project: rtx.Dir()}, a.Name)
	if err != nil || !removed {
		return err
	}
	_, err = fmt.Fprintf(rtx.Stdout, "removed the tool rules for %s from %s\n", a.Name, path)
	return err
}
