package main

import (
	"context"
	"os"

	"github.com/prsuyal/why-diff/internal/cli"
)

func main() {
	workingDirectory, err := os.Getwd()
	if err != nil {
		workingDirectory = "."
	}
	os.Exit(cli.Run(context.Background(), os.Args[1:], cli.Environment{
		Stdin:            os.Stdin,
		Stdout:           os.Stdout,
		Stderr:           os.Stderr,
		WorkingDirectory: workingDirectory,
	}))
}
