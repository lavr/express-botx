package main

import (
	"fmt"
	"os"

	"golang.org/x/term"

	"github.com/lavr/express-bot/internal/cmd"
)

func main() {
	deps := cmd.Deps{
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		Stdin:      os.Stdin,
		IsTerminal: term.IsTerminal(int(os.Stdin.Fd())),
	}
	if err := cmd.Run(os.Args[1:], deps); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
