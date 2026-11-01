package rotinimcp

import (
	"context"
	"fmt"

	"github.com/go-rotini/rotini"
)

var _ rotini.HandlerE = (*rotiniMcpConfigHandler)(nil)

type rotiniMcpConfigHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*rotiniMcpConfigHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	fmt.Fprintln(rtx.Stderr, rtx.Help())
	rtx.HaltWithCode(1)
	return nil
}
