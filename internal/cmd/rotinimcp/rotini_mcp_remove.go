package rotinimcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-rotini/rotini"

	"github.com/go-rotini/mcp/clients"
)

var _ rotini.HandlerE = (*rotiniMcpRemoveHandler)(nil)

type rotiniMcpRemoveHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniMcpRemoveHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniMcpRemoveInputs]()
	if err != nil {
		return err
	}
	f, a := inputs.RotiniMcpRemove.Flags, inputs.RotiniMcpRemove.Arguments
	client := clients.Client(a.Client)
	name := a.Name
	if strings.ContainsAny(name, `/\`) || strings.HasSuffix(name, ".exe") {
		name = serverName(name) // the program it was added for
	}

	dirs := clients.Dirs{Project: rtx.Dir()}
	path, removed, err := clients.Remove(client, clients.Scope(f.Scope), dirs, name)
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("removed %s from %s", name, path)
	if !removed {
		msg = fmt.Sprintf("%s has no server named %s", path, name)
	}
	if _, err := fmt.Fprintln(rtx.Stdout, msg); err != nil {
		return err
	}
	if client != clients.ClaudeCode {
		return nil
	}
	path, removed, err = clients.ClearPermissions(client, dirs, name)
	if err != nil || !removed {
		return err
	}
	_, err = fmt.Fprintf(rtx.Stdout, "removed the tool rules for %s from %s\n", name, path)
	return err
}
