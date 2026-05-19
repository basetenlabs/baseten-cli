package main

import (
	"context"
	"os"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

func main() {
	cmd.VerifyRunners()
	if err := cmd.Execute(context.Background(), cmd.ExecuteOptions{}); err != nil {
		os.Exit(1)
	}
}
