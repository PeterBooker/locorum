package cli

import "fmt"

func runVersion(env *Env) ExitCode {
	_, _ = fmt.Fprintf(env.Stdout, "locorum %s\n", env.Version)
	return ExitOK
}
