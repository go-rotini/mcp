package taskr

import (
	"context"
	"errors"

	"github.com/go-rotini/rotini"
)

var _ rotini.HandlerE = (*taskrFailHandler)(nil)

type taskrFailHandler struct {
	rotini.NoCascadingPreRunE
	rotini.NoPreRunE
	rotini.NoPostRunE
	rotini.NoCascadingPostRunE
}

func (*taskrFailHandler) Run(ctx context.Context, rtx *rotini.Context) error {
	rtx.Record(SkippedChannel, SkippedRecord{Title: "water plants"})
	return errors.New("the list is locked")
}
