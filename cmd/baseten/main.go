package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	cmd.VerifyRunners()
	if err := cmd.Execute(ctx, cmd.ExecuteOptions{}); err != nil {
		os.Exit(1)
	}
}
