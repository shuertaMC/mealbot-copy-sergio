package config

// Version is the application version reported by GET /version. It defaults to
// "dev" and is set at build time with:
//
//	go build -ldflags "-X github.com/johnamadeo/mealbot/internal/config.Version=1.2.3" ./cmd/mealbot
var Version = "dev"
