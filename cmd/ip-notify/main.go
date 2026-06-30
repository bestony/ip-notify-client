package main

import (
	"context"
	"os/signal"
	"syscall"

	"bestony.com/ip-notify-client/internal/cli"
)

var (
	notifyContext  = signal.NotifyContext
	newRootCommand = cli.NewRootCommand
	exitOnError    = cli.ExitOnError
)

func main() {
	ctx, stop := notifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	root := newRootCommand()
	root.SetContext(ctx)
	exitOnError(root.Execute())
}
