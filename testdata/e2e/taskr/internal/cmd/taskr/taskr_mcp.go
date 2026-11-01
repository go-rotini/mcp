package taskr

import (
	"context"

	"github.com/go-rotini/mcp"
	"github.com/go-rotini/rotini"
)

var _ rotini.HandlerE = (*taskrMcpHandler)(nil)

type taskrMcpHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*taskrMcpHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	return mcp.Serve(ctx, []byte(ToolsMCP),
		mcp.WithStdio(rtx.Stdin, rtx.Stdout),
		mcp.WithEnv(rtx.Environ()),
		mcp.WithServingCommand("mcp"))
}
