package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/GrainedLotus515/gobard/internal/bot"
	"github.com/GrainedLotus515/gobard/internal/config"
	"github.com/GrainedLotus515/gobard/internal/logger"
	"github.com/joho/godotenv"
)

func main() {
	// Load .env file (optional, won't error if not present)
	if err := godotenv.Load(); err != nil {
		logger.Debug("No .env file found, using environment variables")
	}

	// Load configuration from environment
	cfg, err := config.Load()
	if err != nil {
		logger.Fatal("Failed to load configuration", "err", err)
	}

	// Create bot instance
	b, err := bot.New(cfg)
	if err != nil {
		logger.Fatal("Failed to create bot", "err", err)
	}

	// Start the bot
	if err := b.Start(); err != nil {
		logger.Fatal("Failed to start bot", "err", err)
	}

	// Wait for interrupt signal
	logger.Info("Bot is running. Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	// Graceful shutdown
	logger.Info("Shutting down...")
	if err := b.Stop(); err != nil {
		logger.Error("Error during shutdown", "err", err)
	}

	logger.Info("Goodbye! 👋")
}
