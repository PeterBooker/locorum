package cli

import "fmt"

func runVersion(env *Env) ExitCode {
	fmt.Fprintf(env.Stdout, "locorum %s\n", env.Version)
	return ExitOK
}
