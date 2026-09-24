package main

import (
	"context"
	"os"

	"github.com/prsuyal/why-diff/internal/hookcli"
)

func main() {
	os.Exit(hookcli.Run(context.Background(), os.Args[1:], os.Stdin, os.Stderr))
}
