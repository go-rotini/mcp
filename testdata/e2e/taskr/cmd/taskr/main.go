package main

import (
	"os"

	"github.com/go-rotini/rotini"

	cmd "example.com/taskr/internal/cmd/taskr"
)

func main() {
	cmd.NewProgram().
		WithReporter(rotini.StructuredReporter(func(*rotini.Context) bool {
			return os.Getenv("TASKR_JSON_ERRORS") == "1"
		})).
		Execute()
}
