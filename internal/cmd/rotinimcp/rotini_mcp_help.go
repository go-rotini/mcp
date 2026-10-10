package rotinimcp

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.Handler = (*rotiniMcpHelpHandler)(nil)

type rotiniMcpHelpHandler struct {
	rotini.NoCascadingPreRun
	rotini.NoPreRun
	rotini.NoPostRun
	rotini.NoCascadingPostRun
}

func (*rotiniMcpHelpHandler) Run(ctx context.Context, rtx *rotini.Context) {
	inputs, err := rtx.Inputs[RotiniMcpHelpInputs]()
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	page, err := Help(inputs.RotiniMcpHelp.Arguments.Command...)
	if err != nil {
		rtx.HaltWith(err)
		return
	}

	if _, err := fmt.Fprintln(rtx.Stdout, page); err != nil {
		rtx.HaltWith(err)
	}
}
