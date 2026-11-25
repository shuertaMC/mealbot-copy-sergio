// Command mealbot is the modular entry point for the Mealbot service.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/johnamadeo/mealbot/internal/app"
)

func main() {
	if err := app.Run(context.Background()); err != nil {
		slog.New(slog.NewTextHandler(os.Stderr, nil)).Error("mealbot stopped", "error", err)
		os.Exit(1)
	}
}
