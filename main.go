package main

import (
	"fmt"
	"os"

	"github.com/mdenizay/peyk/internal/cli"
)

// version is set at build time via -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	if err := cli.Execute(version); err != nil {
		fmt.Fprintln(os.Stderr, "peyk:", err)
		os.Exit(1)
	}
}
