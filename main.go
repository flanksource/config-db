package main

import (
	"os"

	"github.com/flanksource/confighub/cmd"
)

func main() {

	if err := cmd.Root.Execute(); err != nil {
		os.Exit(1)
	}
}
