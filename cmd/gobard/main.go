package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/charmbracelet/log"
	"github.com/joho/godotenv"

	"github.com/GrainedLotus515/gobard/internal/app"
	"github.com/GrainedLotus515/gobard/internal/config"
	"github.com/GrainedLotus515/gobard/internal/health"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Error("GoBard exited", "event", "app.terminal", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 {
		if args[0] != "healthcheck" {
			return fmt.Errorf("unknown command")
		}
		return runHealthcheck(args[1:])
	}
	// An absent optional dotenv file is normal; malformed configuration fails Load.
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("could not load optional dotenv configuration")
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	logger := log.New(os.Stderr)
	if cfg.LogFormat == "json" {
		logger.SetFormatter(log.JSONFormatter)
	}
	if cfg.Debug {
		logger.SetLevel(log.DebugLevel)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	application, err := app.New(ctx, cfg, logger)
	if err != nil {
		return fmt.Errorf("compose application: %w", err)
	}
	defer application.Close()
	server, err := health.Start(cfg.HealthListenAddr, application)
	if err != nil {
		return fmt.Errorf("start health server: %w", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(cleanup); err != nil {
			logger.Error("Health shutdown failed", "event", "health.shutdown", "error", err)
		}
	}()
	if err := application.Start(); err != nil {
		return err
	}
	logger.Info("GoBard v2 running", "event", "app.started", "guild_limit", cfg.MaxGuildSessions, "queue_limit", cfg.QueueLimit, "extraction_limit", cfg.YTDLPMaxConcurrency)
	<-ctx.Done()
	logger.Info("Shutdown requested", "event", "app.shutdown")
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
