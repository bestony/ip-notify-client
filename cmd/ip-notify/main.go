package main

import (
	"context"
	"os/signal"
	"syscall"

	"bestony.com/ip-notify-client/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	root := cli.NewRootCommand()
	root.SetContext(ctx)
	cli.ExitOnError(root.Execute())
}
