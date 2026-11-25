// Command mealbot is the modular entry point for the Mealbot service.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/johnamadeo/mealbot/internal/app"
)

type executeFunc func(context.Context, []string) error

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], app.RunArgs, os.Stderr))
}

func run(ctx context.Context, args []string, execute executeFunc, stderr io.Writer) int {
	if err := execute(ctx, args); err != nil {
		_, _ = fmt.Fprint(stderr, err)
		return 1
	}
	return 0
}
