// Command capacitybench exercises GoBard's per-guild hot path under a fixed
// container quota. It is a deterministic deployment benchmark, not a Discord
// integration test: acquisition and Opus encoding are real, provider retrieval
// uses a local fixture, and the voice writer discards packets.
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

	"github.com/charmbracelet/log"

	"github.com/GrainedLotus515/gobard/internal/cache"
	"github.com/GrainedLotus515/gobard/internal/capacity"
	"github.com/GrainedLotus515/gobard/internal/discordvoice"
	"github.com/GrainedLotus515/gobard/internal/failure"
	"github.com/GrainedLotus515/gobard/internal/playback"
	"github.com/GrainedLotus515/gobard/internal/processlimit"
	"github.com/GrainedLotus515/gobard/internal/session"
	"github.com/GrainedLotus515/gobard/internal/source"
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
	workerFailures         atomic.Uint64
	maximumShutdownNS      atomic.Int64
	frameGaps              [1001]atomic.Uint64
}

type report struct {
	AcquisitionWorkload         string  `json:"acquisition_workload"`
	Profile                     string  `json:"profile"`
	DurationSeconds             float64 `json:"duration_seconds"`
	SimultaneousGuilds          int     `json:"simultaneous_guilds"`
	Frames                      uint64  `json:"frames"`
	AverageCPUPercentOfQuota    float64 `json:"average_cpu_percent_of_quota"`
	PeakCgroupMemoryBytes       uint64  `json:"peak_cgroup_memory_bytes"`
	PeakAnonymousMemoryBytes    uint64  `json:"peak_anonymous_memory_bytes"`
	AddedAnonymousBytesPerGuild uint64  `json:"added_anonymous_bytes_per_guild"`
	MemoryLimitBytes            uint64  `json:"memory_limit_bytes"`
	PeakMemoryPercentOfLimit    float64 `json:"peak_memory_percent_of_limit"`
	MissedThreeFrameDeadlines   uint64  `json:"missed_three_frame_deadlines"`
	MaximumFrameDelayMS         float64 `json:"maximum_frame_delay_ms"`
	AverageFirstFrameLatencyMS  float64 `json:"average_first_frame_latency_ms"`
	MaximumFirstFrameLatencyMS  float64 `json:"maximum_first_frame_latency_ms"`
	YTDLPConfiguredLimit        int     `json:"ytdlp_configured_limit"`
	GoroutinesBefore            int     `json:"goroutines_before"`
	GoroutinesAfter             int     `json:"goroutines_after"`
	TemporaryFilesAfter         int     `json:"temporary_files_after"`
	WorkerFailures              uint64  `json:"worker_failures"`
	MaximumSessionShutdownMS    float64 `json:"maximum_session_shutdown_ms"`
	P99FrameGapMS               int     `json:"p99_frame_gap_ms"`
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
		if err != nil || duration < time.Second || duration > time.Hour {
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
	mediaPath, err := createDeterministicMedia(tempDir, duration+time.Second)
	if err != nil {
		return err
	}

	if err := os.Setenv("GOBARD_BENCH_MEDIA", mediaPath); err != nil {
		return err
	}
	baselineGoroutines := runtime.NumGoroutine()
	baselineAnonymous := readAnonymousMemory()
	startCPU, quotaCPUs := readCPUUsage()
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var measured measurements
	peakMemory := sampleMemory(ctx)
	limiter := processlimit.New(cfg.ytdlpConcurrency)
	opener, err := source.NewYouTubeOpener(limiter, cacheStore, log.New(io.Discard))
	if err != nil {
		return err
	}
	var workers sync.WaitGroup
	for guild := range cfg.guilds {
		workers.Add(1)
		go runGuild(ctx, &workers, guild, opener, cacheStore, &measured)
	}
	workers.Add(1)
	go runCachePressure(ctx, &workers, cacheStore, &measured)

	startedAt := time.Now()
	workers.Wait()
	elapsed := time.Since(startedAt)
	endCPU, _ := readCPUUsage()
	memory := <-peakMemory
	var addedAnonymous uint64
	if memory.anonymous > baselineAnonymous {
		// #nosec G115 -- selectedProfile admits only 1, 4, 12 or 24 guilds.
		addedAnonymous = (memory.anonymous - baselineAnonymous) / uint64(cfg.guilds)
	}

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
		AcquisitionWorkload:         "offline extractor -> 8 MiB ring -> FFmpeg -> 128 kbit/s Opus -> paced fake voice; 64 kbit/s input",
		Profile:                     cfg.name,
		DurationSeconds:             elapsed.Seconds(),
		SimultaneousGuilds:          cfg.guilds,
		Frames:                      frames,
		AverageCPUPercentOfQuota:    cpuPercent(startCPU, endCPU, elapsed, quotaCPUs),
		PeakCgroupMemoryBytes:       memory.total,
		PeakAnonymousMemoryBytes:    memory.anonymous,
		AddedAnonymousBytesPerGuild: addedAnonymous,
		MemoryLimitBytes:            cfg.memoryBytes,
		PeakMemoryPercentOfLimit:    percent(float64(memory.total), float64(cfg.memoryBytes)),
		MissedThreeFrameDeadlines:   measured.missedDeadlines.Load(),
		MaximumFrameDelayMS:         durationMS(time.Duration(measured.maximumDeadlineDelayNS.Load())),
		AverageFirstFrameLatencyMS:  durationMS(time.Duration(measured.firstFrameTotalNS.Load() / int64(cfg.guilds))),
		MaximumFirstFrameLatencyMS:  durationMS(time.Duration(measured.maximumFirstFrameNS.Load())),
		YTDLPConfiguredLimit:        cfg.ytdlpConcurrency,
		GoroutinesBefore:            baselineGoroutines,
		GoroutinesAfter:             afterGoroutines,
		TemporaryFilesAfter:         temporaryFiles,
		WorkerFailures:              measured.workerFailures.Load(),
		MaximumSessionShutdownMS:    durationMS(time.Duration(measured.maximumShutdownNS.Load())),
		P99FrameGapMS:               frameGapPercentile(&measured, 99),
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode benchmark report: %w", err)
	}
	fmt.Println(string(encoded))
	return validateReport(result, cfg)
}

func validateReport(result report, cfg profile) error {
	var failures []error
	if result.AverageCPUPercentOfQuota >= 80 {
		failures = append(failures, fmt.Errorf("average CPU %.2f%% is not below 80%% of quota", result.AverageCPUPercentOfQuota))
	}
	if result.PeakMemoryPercentOfLimit >= 75 {
		failures = append(failures, fmt.Errorf("peak cgroup memory %.2f%% is not below 75%% of memory limit", result.PeakMemoryPercentOfLimit))
	}
	if result.MissedThreeFrameDeadlines != 0 {
		failures = append(failures, fmt.Errorf("%d frame deadlines exceeded three intervals", result.MissedThreeFrameDeadlines))
	}
	if result.GoroutinesAfter > result.GoroutinesBefore+2 {
		failures = append(failures, fmt.Errorf("goroutines grew from %d to %d", result.GoroutinesBefore, result.GoroutinesAfter))
	}
	if result.TemporaryFilesAfter != 0 {
		failures = append(failures, fmt.Errorf("%d cache temporary files remained", result.TemporaryFilesAfter))
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
	case "single":
		return profile{name: "single", guilds: 1, ytdlpConcurrency: 4, memoryBytes: 1 << 30}, nil
	case "small":
		return profile{name: "small", guilds: 4, ytdlpConcurrency: 4, memoryBytes: 1 << 30}, nil
	case "medium":
		return profile{name: "medium", guilds: 12, ytdlpConcurrency: 8, memoryBytes: 2 << 30}, nil
	case "large":
		return profile{name: "large", guilds: 24, ytdlpConcurrency: 12, memoryBytes: 4 << 30}, nil
	case "reduced":
		memoryBytes := uint64(512 * 1024 * 1024)
		guilds, err := capacity.GuildLimit(memoryBytes, capacity.DefaultHeadroom(memoryBytes), capacity.DefaultPerGuildReservation, 16)
		if err != nil {
			return profile{}, fmt.Errorf("derive reduced profile: %w", err)
		}
		return profile{name: "reduced", guilds: guilds, ytdlpConcurrency: 4, memoryBytes: memoryBytes}, nil
	default:
		return profile{}, fmt.Errorf("unknown GOBARD_BENCH_PROFILE %q", name)
	}
}

// runGuild exercises the actual v2 coordinator, decoder, encoder, controls and
// cancellation path. Only network delivery and provider work are simulated.
func runGuild(ctx context.Context, workers *sync.WaitGroup, guild int, opener playback.Opener, cacheStore *cache.Cache, measured *measurements) {
	defer workers.Done()
	engine, err := playback.New(opener, log.New(io.Discard))
	if err != nil {
		measured.workerFailures.Add(1)
		return
	}
	delivery, err := discordvoice.NewOutput(discardPackets{})
	if err != nil {
		measured.workerFailures.Add(1)
		return
	}
	defer func() {
		if err := delivery.Close(); err != nil {
			measured.workerFailures.Add(1)
		}
	}()
	output := &probeOutput{started: time.Now(), measured: measured, cache: cacheStore, guild: guild, delivery: delivery}
	target, err := session.New(ctx, fmt.Sprintf("bench-%d", guild), engine, output, 8)
	if err != nil {
		measured.workerFailures.Add(1)
		return
	}
	defer func() {
		start := time.Now()
		target.Close()
		updateMaximum(&measured.maximumShutdownNS, time.Since(start).Nanoseconds())
	}()
	for _, op := range []session.Operation{{Action: session.Volume, Position: 85}, {Action: session.Loop}} {
		if _, err := target.Apply(ctx, op); err != nil {
			measured.workerFailures.Add(1)
			return
		}
	}
	_, err = target.Enqueue(ctx, playback.Request{Provider: "fixture", MediaID: fmt.Sprintf("https://www.youtube.com/watch?v=fixture%04d", guild)})
	if err != nil {
		measured.workerFailures.Add(1)
		return
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snapshot, err := target.Apply(ctx, session.Operation{Action: session.Inspect})
			if ctx.Err() != nil {
				return
			}
			if err != nil || snapshot.Last.Failure != nil {
				measured.workerFailures.Add(1)
				return
			}
		}
	}
}

type probeOutput struct {
	started, last time.Time
	delivery      *discordvoice.Output
	measured      *measurements
	cache         *cache.Cache
	guild         int
	frames        uint64
}

func (o *probeOutput) Send(ctx context.Context, packet []byte) error {
	if err := o.delivery.Send(ctx, packet); err != nil {
		return err
	}
	now := time.Now()
	if o.frames == 0 {
		latency := now.Sub(o.started).Nanoseconds()
		o.measured.firstFrameTotalNS.Add(latency)
		updateMaximum(&o.measured.maximumFirstFrameNS, latency)
	} else {
		gap := now.Sub(o.last)
		updateMaximum(&o.measured.maximumDeadlineDelayNS, gap.Nanoseconds())
		o.measured.frameGaps[min(gap.Milliseconds(), 1000)].Add(1)
		if gap > 3*opusInterval {
			o.measured.missedDeadlines.Add(1)
		}
	}
	o.last = now
	o.frames++
	o.measured.frames.Add(1)
	if o.frames%25 == 0 {
		key := cache.GenerateKey(fmt.Sprintf("guild-%d-cache-%d", o.guild, o.frames%3))
		if lease, ok := o.cache.Acquire(key); ok {
			lease.Release()
		}
	}
	return nil
}

type discardPackets struct{}

func (discardPackets) Write(packet []byte) (int, error) { return len(packet), nil }
func (discardPackets) SetWriteDeadline(time.Time) error { return nil }
func (discardPackets) Close() error                     { return nil }

func frameGapPercentile(m *measurements, percentile uint64) int {
	var total uint64
	for i := range m.frameGaps {
		total += m.frameGaps[i].Load()
	}
	if total == 0 {
		return 0
	}
	target := (total*percentile + 99) / 100
	var seen uint64
	for i := range m.frameGaps {
		seen += m.frameGaps[i].Load()
		if seen >= target {
			return i
		}
	}
	return 1000
}

func createDeterministicMedia(dir string, duration time.Duration) (string, error) {
	path := filepath.Join(dir, "deterministic.opus")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext( // #nosec G204 G702 -- no shell; duration is parsed/bounded then formatted numerically, path is a private fixture.
		ctx,
		"ffmpeg",
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "sine=frequency=997:sample_rate=48000",
		"-t", strconv.FormatFloat(duration.Seconds(), 'f', 3, 64), "-ac", "2", "-c:a", "libopus", "-b:a", "64k", "-vbr", "off", "-y", path,
	)
	var stderr failure.Diagnostic
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		return "", stderr.Failure("fixture", failure.Codec, err)
	}
	return path, nil
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
			if err := writeCacheFixture(ctx, cacheStore, key); err != nil {
				if errors.Is(err, cache.ErrBusy) {
					continue
				}
				if ctx.Err() == nil {
					measured.workerFailures.Add(1)
				}
				return
			}
		}
	}
}

func seedCache(cacheStore *cache.Cache) error {
	for index := range 3 {
		key := cache.GenerateKey(fmt.Sprintf("guild-0-cache-%d", index))
		if err := writeCacheFixture(context.Background(), cacheStore, key); err != nil {
			return fmt.Errorf("seed benchmark cache: %w", err)
		}
	}
	return nil
}

func writeCacheFixture(ctx context.Context, store *cache.Cache, key string) error {
	if lease, ok := store.Acquire(key); ok {
		lease.Release()
		return nil
	}
	w, err := store.TryBegin(key, 4096)
	if err != nil {
		return err
	}
	var data [4096]byte
	if _, err := w.Write(data[:]); err != nil {
		return errors.Join(err, w.Abort())
	}
	return w.Commit(ctx)
}

type memorySample struct{ total, anonymous uint64 }

func sampleMemory(ctx context.Context) <-chan memorySample {
	result := make(chan memorySample, 1)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		var peak memorySample
		for {
			peak.total = max(peak.total, readUintFile("/sys/fs/cgroup/memory.current"))
			peak.anonymous = max(peak.anonymous, readAnonymousMemory())
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

func readAnonymousMemory() uint64 {
	data, err := os.ReadFile("/sys/fs/cgroup/memory.stat")
	if err != nil {
		return 0
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "anon" {
			value, err := strconv.ParseUint(fields[1], 10, 64)
			if err == nil {
				return value
			}
		}
	}
	return 0
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
