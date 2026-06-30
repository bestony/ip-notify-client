package main

import (
	"context"
	"os"
	"syscall"
	"testing"

	"github.com/spf13/cobra"
)

func TestMainFunctionIsReachable(t *testing.T) {
	originalNotifyContext := notifyContext
	originalNewRootCommand := newRootCommand
	originalExitOnError := exitOnError
	t.Cleanup(func() {
		notifyContext = originalNotifyContext
		newRootCommand = originalNewRootCommand
		exitOnError = originalExitOnError
	})

	stopCalled := false
	executeCalled := false
	exitCalled := false
	notifyContext = func(parent context.Context, signals ...os.Signal) (context.Context, context.CancelFunc) {
		if len(signals) != 2 || signals[0] != syscall.SIGINT || signals[1] != syscall.SIGTERM {
			t.Fatalf("unexpected signals: %#v", signals)
		}
		return parent, func() { stopCalled = true }
	}
	newRootCommand = func() *cobra.Command {
		return &cobra.Command{
			Use: "ip-notify",
			RunE: func(cmd *cobra.Command, _ []string) error {
				if cmd.Context() == nil {
					t.Fatal("expected command context")
				}
				executeCalled = true
				return nil
			},
		}
	}
	exitOnError = func(err error) {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		exitCalled = true
	}

	main()
	if !executeCalled {
		t.Fatal("expected root command to execute")
	}
	if !exitCalled {
		t.Fatal("expected exit handler to be called")
	}
	if !stopCalled {
		t.Fatal("expected signal stop function to be called")
	}
}
