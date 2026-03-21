package main

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/term"

	"github.com/lavr/express-botx/internal/cmd"
)

// version is set at build time via -ldflags.
var version = "dev"

func main() {
	cmd.Version = version
	deps := cmd.Deps{
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Stdin:      os.Stdin,
		IsTerminal: term.IsTerminal(int(os.Stdin.Fd())),
	}
	if err := cmd.Run(os.Args[1:], deps); err != nil {
		var exitErr *cmd.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
