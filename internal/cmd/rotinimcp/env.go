package rotinimcp

import (
	"fmt"
	"strings"
)

// checkEnv refuses an --env value that isn't KEY=value.
func checkEnv(values []string) error {
	for _, kv := range values {
		if k, _, ok := strings.Cut(kv, "="); !ok || k == "" {
			return fmt.Errorf("--env %q: want KEY=value", kv)
		}
	}
	return nil
}
