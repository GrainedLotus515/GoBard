// Command capacitybench exercises GoBard's per-guild hot path under a fixed
// container quota. It is a deterministic deployment benchmark, not a Discord
// integration test: Opus encoding is real while voice delivery and yt-dlp
// process work are intentionally stubbed.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hraban/opus"

	"github.com/GrainedLotus515/gobard/internal/cache"
	"github.com/GrainedLotus515/gobard/internal/player"
	"github.com/GrainedLotus515/gobard/internal/processlimit"
)

const opusInterval = 20 * time.Millisecond

type profile struct {
	name             string
	guilds           int
	ytdlpConcurrency int
	memoryBytes      uint64
}

type measurements struct {
	frames                 atomic.Uint64
	missedDeadlines        atomic.Uint64
	maximumDeadlineDelayNS atomic.Int64
	firstFrameTotalNS      atomic.Int64
	maximumFirstFrameNS    atomic.Int64
	maximumYTDLP           atomic.Int64
	maximumBackground      atomic.Int64
	maximumInteractiveNS   atomic.Int64
	workerFailures         atomic.Uint64
}

type report struct {
	Profile                    string  `json:"profile"`
	DurationSeconds            float64 `json:"duration_seconds"`
	SimultaneousGuilds         int     `json:"simultaneous_guilds"`
	Frames                     uint64  `json:"frames"`
	AverageCPUPercentOfQuota   float64 `json:"average_cpu_percent_of_quota"`
	PeakRSSBytes               uint64  `json:"peak_rss_bytes"`
	MemoryLimitBytes           uint64  `json:"memory_limit_bytes"`
	PeakRSSPercentOfLimit      float64 `json:"peak_rss_percent_of_limit"`
	MissedThreeFrameDeadlines  uint64  `json:"missed_three_frame_deadlines"`
	MaximumFrameDelayMS        float64 `json:"maximum_frame_delay_ms"`
	AverageFirstFrameLatencyMS float64 `json:"average_first_frame_latency_ms"`
	MaximumFirstFrameLatencyMS float64 `json:"maximum_first_frame_latency_ms"`
	MaximumYTDLPWork           int64   `json:"maximum_stubbed_ytdlp_work"`
	YTDLPConfiguredLimit       int     `json:"ytdlp_configured_limit"`
	MaximumCacheDownloads      int64   `json:"maximum_cache_downloads"`
	MaximumInteractiveWaitMS   float64 `json:"maximum_interactive_wait_ms"`
	GoroutinesBefore           int     `json:"goroutines_before"`
	GoroutinesAfter            int     `json:"goroutines_after"`
	TemporaryFilesAfter        int     `json:"temporary_files_after"`
	WorkerFailures             uint64  `json:"worker_failures"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "capacity benchmark failed:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := selectedProfile(os.Getenv("GOBARD_BENCH_PROFILE"))
	if err != nil {
		return err
	}
	duration := 15 * time.Minute
	if raw := os.Getenv("GOBARD_BENCH_DURATION"); raw != "" {
		duration, err = time.ParseDuration(raw)
		if err != nil || duration < time.Second {
			return fmt.Errorf("invalid GOBARD_BENCH_DURATION %q", raw)
		}
	}

	tempDir, err := os.MkdirTemp("", "gobard-capacity-")
	if err != nil {
		return fmt.Errorf("create benchmark cache: %w", err)
	}
	defer os.RemoveAll(tempDir)
	cacheStore, err := cache.NewCache(tempDir, 256*1024)
	if err != nil {
		return err
	}
	if err := seedCache(cacheStore); err != nil {
		return err
	}
	mediaPath, err := createDeterministicMedia(tempDir)
	if err != nil {
		return err
	}

	baselineGoroutines := runtime.NumGoroutine()
	startCPU, quotaCPUs := readCPUUsage()
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var measured measurements
	peakMemory := sampleMemory(ctx)
	limiter := processlimit.New(cfg.ytdlpConcurrency)
	var workers sync.WaitGroup
	for guild := range cfg.guilds {
		workers.Add(1)
		go runGuild(ctx, &workers, guild, mediaPath, cacheStore, &measured)
	}
	runLimiterPressure(ctx, &workers, limiter, &measured)
	workers.Add(1)
	go runCachePressure(ctx, &workers, cacheStore, &measured)

	startedAt := time.Now()
	workers.Wait()
	elapsed := time.Since(startedAt)
	endCPU, _ := readCPUUsage()
	peakRSS := <-peakMemory

	if err := cacheStore.Clear(); err != nil {
		return fmt.Errorf("cache leases remained after shutdown: %w", err)
	}
	temporaryFiles, err := countTemporaryFiles(tempDir)
	if err != nil {
		return err
	}
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	afterGoroutines := runtime.NumGoroutine()

	frames := measured.frames.Load()
	result := report{
		Profile:                    cfg.name,
		DurationSeconds:            elapsed.Seconds(),
		SimultaneousGuilds:         cfg.guilds,
		Frames:                     frames,
		AverageCPUPercentOfQuota:   cpuPercent(startCPU, endCPU, elapsed, quotaCPUs),
		PeakRSSBytes:               peakRSS,
		MemoryLimitBytes:           cfg.memoryBytes,
		PeakRSSPercentOfLimit:      percent(float64(peakRSS), float64(cfg.memoryBytes)),
		MissedThreeFrameDeadlines:  measured.missedDeadlines.Load(),
		MaximumFrameDelayMS:        durationMS(time.Duration(measured.maximumDeadlineDelayNS.Load())),
		AverageFirstFrameLatencyMS: durationMS(time.Duration(measured.firstFrameTotalNS.Load() / int64(cfg.guilds))),
		MaximumFirstFrameLatencyMS: durationMS(time.Duration(measured.maximumFirstFrameNS.Load())),
		MaximumYTDLPWork:           measured.maximumYTDLP.Load(),
		YTDLPConfiguredLimit:       cfg.ytdlpConcurrency,
		MaximumCacheDownloads:      measured.maximumBackground.Load(),
		MaximumInteractiveWaitMS:   durationMS(time.Duration(measured.maximumInteractiveNS.Load())),
		GoroutinesBefore:           baselineGoroutines,
		GoroutinesAfter:            afterGoroutines,
		TemporaryFilesAfter:        temporaryFiles,
		WorkerFailures:             measured.workerFailures.Load(),
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode benchmark report: %w", err)
	}
	fmt.Println(string(encoded))

	var failures []error
	if result.AverageCPUPercentOfQuota >= 80 {
		failures = append(failures, fmt.Errorf("average CPU %.2f%% is not below 80%% of quota", result.AverageCPUPercentOfQuota))
	}
	if result.PeakRSSPercentOfLimit >= 75 {
		failures = append(failures, fmt.Errorf("peak RSS %.2f%% is not below 75%% of memory limit", result.PeakRSSPercentOfLimit))
	}
	if result.MissedThreeFrameDeadlines != 0 {
		failures = append(failures, fmt.Errorf("%d frame deadlines exceeded three intervals", result.MissedThreeFrameDeadlines))
	}
	if result.MaximumYTDLPWork > int64(cfg.ytdlpConcurrency) {
		failures = append(failures, fmt.Errorf("stubbed yt-dlp work exceeded configured limit"))
	}
	if result.MaximumCacheDownloads > 1 {
		failures = append(failures, fmt.Errorf("cache downloads exceeded global limit"))
	}
	if afterGoroutines > baselineGoroutines+2 {
		failures = append(failures, fmt.Errorf("goroutines grew from %d to %d", baselineGoroutines, afterGoroutines))
	}
	if temporaryFiles != 0 {
		failures = append(failures, fmt.Errorf("%d cache temporary files remained", temporaryFiles))
	}
	if result.WorkerFailures != 0 {
		failures = append(failures, fmt.Errorf("%d benchmark workers failed", result.WorkerFailures))
	}
	if baseline := firstFrameBaseline(cfg.name); baseline > 0 && result.AverageFirstFrameLatencyMS > baseline*1.10 {
		failures = append(failures, fmt.Errorf("first-frame latency %.3fms exceeds baseline %.3fms by more than 10%%", result.AverageFirstFrameLatencyMS, baseline))
	}
	return errors.Join(failures...)
}

func selectedProfile(name string) (profile, error) {
	switch strings.ToLower(name) {
	case "small":
		return profile{name: "small", guilds: 4, ytdlpConcurrency: 4, memoryBytes: 1 << 30}, nil
	case "medium":
		return profile{name: "medium", guilds: 12, ytdlpConcurrency: 8, memoryBytes: 2 << 30}, nil
	case "large":
		return profile{name: "large", guilds: 24, ytdlpConcurrency: 12, memoryBytes: 4 << 30}, nil
	default:
		return profile{}, fmt.Errorf("unknown GOBARD_BENCH_PROFILE %q", name)
	}
}

func runGuild(
	ctx context.Context,
	workers *sync.WaitGroup,
	guild int,
	mediaPath string,
	cacheStore *cache.Cache,
	measured *measurements,
) {
	defer workers.Done()
	launchedAt := time.Now()
	ffmpegCtx, stopFFmpeg := context.WithCancel(ctx)
	defer stopFFmpeg()
	ffmpeg := exec.CommandContext( // #nosec G204 -- fixed executable and arguments; path is an agent-created fixture.
		ffmpegCtx,
		"ffmpeg",
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-threads", "1", "-filter_threads", "1",
		"-stream_loop", "-1", "-i", mediaPath,
		"-f", "s16le", "-ar", "48000", "-ac", "2", "pipe:1",
	)
	stdout, err := ffmpeg.StdoutPipe()
	if err != nil {
		measured.workerFailures.Add(1)
		return
	}
	if err := ffmpeg.Start(); err != nil {
		measured.workerFailures.Add(1)
		return
	}
	defer func() {
		stopFFmpeg()
		if err := ffmpeg.Wait(); err != nil && ctx.Err() == nil {
			measured.workerFailures.Add(1)
		}
	}()

	encoder, err := opus.NewEncoder(48000, 2, opus.AppAudio)
	if err != nil {
		measured.workerFailures.Add(1)
		return
	}
	if err := encoder.SetBitrate(128000); err != nil {
		measured.workerFailures.Add(1)
		return
	}
	pcmBytes := make([]byte, 960*2*2)
	pcm := make([]int16, 960*2)
	opusBuffer := make([]byte, 4000)
	queue := player.NewQueue()
	for track := range 8 {
		queue.Add(&player.Track{ID: fmt.Sprintf("%d-%d", guild, track), Title: "deterministic", URL: fmt.Sprintf("https://www.youtube.com/watch?v=%d-%d", guild, track)})
	}
	queue.Next()

	var localFrames uint64
	encodeFrame := func() bool {
		if _, readErr := io.ReadFull(stdout, pcmBytes); readErr != nil {
			if ctx.Err() == nil {
				measured.workerFailures.Add(1)
			}
			return false
		}
		for index := range pcm {
			pcm[index] = int16(pcmBytes[index*2]) | int16(pcmBytes[index*2+1])<<8
		}
		applyVolume(pcm, 85)
		n, encodeErr := encoder.Encode(pcm, opusBuffer)
		if encodeErr != nil || n == 0 {
			measured.workerFailures.Add(1)
			return false
		}
		localFrames++
		measured.frames.Add(1)
		return true
	}
	if !encodeFrame() {
		return
	}
	firstLatency := time.Since(launchedAt).Nanoseconds()
	measured.firstFrameTotalNS.Add(firstLatency)
	updateMaximum(&measured.maximumFirstFrameNS, firstLatency)
	lastFrame := time.Now()
	ticker := time.NewTicker(opusInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			delay := now.Sub(lastFrame)
			lastFrame = now
			updateMaximum(&measured.maximumDeadlineDelayNS, delay.Nanoseconds())
			if delay > 3*opusInterval {
				measured.missedDeadlines.Add(1)
			}
			if !encodeFrame() {
				return
			}
			if localFrames%25 == 0 {
				queue.ToggleLoop()
				queue.Snapshot()
				key := cache.GenerateKey(fmt.Sprintf("guild-%d-cache-%d", guild, localFrames%3))
				if lease, ok := cacheStore.Acquire(key); ok {
					lease.Release()
				}
			}
		}
	}
}

func createDeterministicMedia(dir string) (string, error) {
	path := filepath.Join(dir, "deterministic.opus")
	cmd := exec.Command( // #nosec G204 -- fixed executable and arguments; path is inside the private temporary directory.
		"ffmpeg",
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "sine=frequency=997:sample_rate=48000",
		"-t", "5", "-ac", "2", "-c:a", "libopus", "-b:a", "128k", "-y", path,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("create deterministic media: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return path, nil
}

func runLimiterPressure(ctx context.Context, workers *sync.WaitGroup, limiter *processlimit.Limiter, measured *measurements) {
	var active atomic.Int64
	var background atomic.Int64
	for range limiter.NonInteractiveCapacity() {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ctx.Err() == nil {
				release, err := limiter.AcquireClass(ctx, processlimit.Bulk)
				if err != nil {
					return
				}
				current := active.Add(1)
				updateMaximum(&measured.maximumYTDLP, current)
				select {
				case <-ctx.Done():
				case <-time.After(10 * time.Millisecond):
				}
				active.Add(-1)
				release()
			}
		}()
	}
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ctx.Err() == nil {
				release, err := limiter.AcquireClass(ctx, processlimit.Background)
				if err != nil {
					return
				}
				currentBackground := background.Add(1)
				current := active.Add(1)
				updateMaximum(&measured.maximumBackground, currentBackground)
				updateMaximum(&measured.maximumYTDLP, current)
				select {
				case <-ctx.Done():
				case <-time.After(15 * time.Millisecond):
				}
				background.Add(-1)
				active.Add(-1)
				release()
			}
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				started := time.Now()
				release, err := limiter.AcquireClass(ctx, processlimit.Interactive)
				if err != nil {
					return
				}
				updateMaximum(&measured.maximumInteractiveNS, time.Since(started).Nanoseconds())
				current := active.Add(1)
				updateMaximum(&measured.maximumYTDLP, current)
				active.Add(-1)
				release()
			}
		}
	}()
}

func runCachePressure(ctx context.Context, workers *sync.WaitGroup, cacheStore *cache.Cache, measured *measurements) {
	defer workers.Done()
	ticker := time.NewTicker(15 * time.Millisecond)
	defer ticker.Stop()
	iteration := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			iteration++
			key := cache.GenerateKey(fmt.Sprintf("pressure-%d", iteration%96))
			if _, err := cacheStore.GetOrCreate(key, func(path string) error {
				return os.WriteFile(path, make([]byte, 4096), 0o600)
			}); err != nil {
				measured.workerFailures.Add(1)
				return
			}
		}
	}
}

func seedCache(cacheStore *cache.Cache) error {
	for index := range 3 {
		key := cache.GenerateKey(fmt.Sprintf("guild-0-cache-%d", index))
		if _, err := cacheStore.GetOrCreate(key, func(path string) error {
			return os.WriteFile(path, make([]byte, 4096), 0o600)
		}); err != nil {
			return fmt.Errorf("seed benchmark cache: %w", err)
		}
	}
	return nil
}

func applyVolume(samples []int16, volume int32) {
	gain := volume * 32768 / 100
	for index, sample := range samples {
		product := int32(sample) * gain
		if product < 0 {
			// #nosec G115 -- Q15 scaling cannot exceed the int16 input range.
			samples[index] = int16(-((-product) >> 15))
		} else {
			// #nosec G115 -- Q15 scaling cannot exceed the int16 input range.
			samples[index] = int16(product >> 15)
		}
	}
}

func sampleMemory(ctx context.Context) <-chan uint64 {
	result := make(chan uint64, 1)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		var peak uint64
		for {
			if current := readUintFile("/sys/fs/cgroup/memory.current"); current > peak {
				peak = current
			}
			select {
			case <-ctx.Done():
				result <- peak
				return
			case <-ticker.C:
			}
		}
	}()
	return result
}

func readCPUUsage() (uint64, float64) {
	contents, err := os.ReadFile("/sys/fs/cgroup/cpu.stat")
	if err != nil {
		return 0, float64(runtime.GOMAXPROCS(0))
	}
	var usage uint64
	for line := range strings.SplitSeq(string(contents), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "usage_usec" {
			parsed, parseErr := strconv.ParseUint(fields[1], 10, 64)
			if parseErr == nil {
				usage = parsed
			}
		}
	}
	quota := float64(runtime.GOMAXPROCS(0))
	if cpuMax, err := os.ReadFile("/sys/fs/cgroup/cpu.max"); err == nil {
		fields := strings.Fields(string(cpuMax))
		if len(fields) == 2 && fields[0] != "max" {
			limit, limitErr := strconv.ParseFloat(fields[0], 64)
			period, periodErr := strconv.ParseFloat(fields[1], 64)
			if limitErr == nil && periodErr == nil && period > 0 {
				quota = limit / period
			}
		}
	}
	return usage, quota
}

func cpuPercent(start, end uint64, elapsed time.Duration, quota float64) float64 {
	if end <= start || elapsed <= 0 || quota <= 0 {
		return 0
	}
	return float64(end-start) / float64(elapsed.Microseconds()) / quota * 100
}

func readUintFile(path string) uint64 {
	contents, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(contents)), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func updateMaximum(target *atomic.Int64, value int64) {
	for {
		current := target.Load()
		if value <= current || target.CompareAndSwap(current, value) {
			return
		}
	}
}

func countTemporaryFiles(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") || strings.Contains(entry.Name(), ".part") {
			count++
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return 0, fmt.Errorf("unexpected symlink in benchmark cache: %s", filepath.Join(dir, entry.Name()))
		}
	}
	return count, nil
}

func firstFrameBaseline(profileName string) float64 {
	name := "GOBARD_BENCH_BASELINE_" + strings.ToUpper(profileName) + "_MS"
	value, err := strconv.ParseFloat(os.Getenv(name), 64)
	if err != nil {
		return 0
	}
	return value
}

func durationMS(duration time.Duration) float64 { return float64(duration) / float64(time.Millisecond) }

func percent(value, limit float64) float64 {
	if limit <= 0 {
		return 0
	}
	return value / limit * 100
}
