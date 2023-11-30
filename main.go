package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/flanksource/config-db/cmd"
)

func main() {
	if err := cmd.Root.ExecuteContext(newCancelableContext()); err != nil {
		os.Exit(1)
	}
}

func newCancelableContext() context.Context {
	doneCh := make(chan os.Signal, 1)
	signal.Notify(doneCh, os.Interrupt)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		<-doneCh
		cancel()
	}()

	return ctx
}
