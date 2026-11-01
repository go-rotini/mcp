package rotinimcp

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.HandlerE = (*rotiniMcpHelpHandler)(nil)

type rotiniMcpHelpHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniMcpHelpHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	inputs, err := rtx.Inputs[RotiniMcpHelpInputs]()
	if err != nil {
		return err
	}

	page, err := rtx.HelpFor(inputs.RotiniMcpHelp.Arguments.Command...)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintln(rtx.Stdout, page)
	return err
}
