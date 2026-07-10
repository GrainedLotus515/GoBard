package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/GrainedLotus515/gobard/internal/bot"
	"github.com/GrainedLotus515/gobard/internal/config"
	"github.com/GrainedLotus515/gobard/internal/health"
	"github.com/GrainedLotus515/gobard/internal/logger"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		logger.Fatal("GoBard exited", "err", err)
	}
}

func run(args []string) error {
	if len(args) > 0 {
		if args[0] != "healthcheck" {
			return fmt.Errorf("unknown command %q", args[0])
		}
		return runHealthcheck(args[1:])
	}

	// Load .env file (optional, won't error if not present)
	if err := godotenv.Load(); err != nil {
		logger.Debug("No .env file found, using environment variables")
	}

	// Load configuration from environment
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	// Set debug mode based on config
	logger.SetDebugMode(cfg.Debug)

	// Create bot instance
	b, err := bot.New(cfg)
	if err != nil {
		return fmt.Errorf("create bot: %w", err)
	}

	healthServer, err := health.Start(cfg.HealthListenAddr, b)
	if err != nil {
		return fmt.Errorf("start health server: %w", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := healthServer.Shutdown(ctx); err != nil {
			logger.Error("Error stopping health server", "err", err)
		}
	}()

	// Start the bot
	if err := b.Start(); err != nil {
		return fmt.Errorf("start bot: %w", err)
	}

	// Wait for interrupt signal
	logger.Info("Bot is running. Press CTRL-C to exit.")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	defer stop()
	<-ctx.Done()

	// Graceful shutdown
	logger.Info("Shutting down...")
	if err := b.Stop(); err != nil {
		logger.Error("Error during shutdown", "err", err)
	}

	logger.Info("Goodbye! 👋")
	return nil
}

func runHealthcheck(args []string) error {
	flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	url := flags.String("url", "http://127.0.0.1:8080/ready", "health endpoint URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("healthcheck does not accept positional arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return health.Probe(ctx, *url)
}
