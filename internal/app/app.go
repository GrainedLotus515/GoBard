// Package app composes the runtime and owns shutdown order. SDK types never
// leave the Discord adapter; sources and sessions depend on consumed contracts.
package app

import (
	"context"
	"errors"
	"os/exec"

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/bot"
	"github.com/GrainedLotus515/gobard/internal/cache"
	"github.com/GrainedLotus515/gobard/internal/config"
	"github.com/GrainedLotus515/gobard/internal/health"
	"github.com/GrainedLotus515/gobard/internal/playback"
	"github.com/GrainedLotus515/gobard/internal/processlimit"
	"github.com/GrainedLotus515/gobard/internal/session"
	"github.com/GrainedLotus515/gobard/internal/source"
)

type App struct {
	discord *bot.Runtime
	cached  *source.Cached
}

func New(ctx context.Context, cfg *config.Config, logger *log.Logger) (*App, error) {
	if ctx == nil || cfg == nil || logger == nil {
		return nil, errors.New("application dependencies required")
	}
	for _, name := range []string{"ffmpeg", "yt-dlp", "deno"} {
		if _, err := exec.LookPath(name); err != nil {
			return nil, errors.New("required runtime tool unavailable: " + name)
		}
	}
	store, err := cache.NewCache(cfg.CacheDir, cfg.CacheLimit)
	if err != nil {
		return nil, err
	}
	limiter := processlimit.New(cfg.YTDLPMaxConcurrency)
	resolver, err := source.NewResolver(limiter, cfg.MaxPlaylistTracks)
	if err != nil {
		return nil, err
	}
	opener, err := source.NewYouTubeOpener(limiter)
	if err != nil {
		return nil, err
	}
	cached, err := source.NewCached(ctx, store, opener, opener, logger)
	if err != nil {
		return nil, err
	}
	engine, err := playback.New(cached, logger)
	if err != nil {
		cached.Close()
		return nil, err
	}
	sessions, err := session.NewManager(ctx, engine, cfg.MaxGuildSessions, cfg.QueueLimit, session.Defaults{Volume: cfg.DefaultVolume, Ducking: cfg.ReduceVolumeOnVoice, DuckTarget: cfg.ReduceVolumeOnVoiceTarget})
	if err != nil {
		cached.Close()
		return nil, err
	}
	discord, err := bot.NewRuntime(ctx, cfg, sessions, resolver, logger)
	if err != nil {
		sessions.Close()
		cached.Close()
		return nil, err
	}
	return &App{discord: discord, cached: cached}, nil
}

func (a *App) Start() error                    { return a.discord.Start() }
func (a *App) Close()                          { a.discord.Close(); a.cached.Close() }
func (a *App) Live() error                     { return a.discord.Live() }
func (a *App) Ready(ctx context.Context) error { return a.discord.Ready(ctx) }
func (a *App) Dimensions() health.Dimensions   { return a.discord.Dimensions() }
