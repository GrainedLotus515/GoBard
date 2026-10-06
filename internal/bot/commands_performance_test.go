package bot

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func syntheticGuildSession(count int) *discordgo.Session {
	state := discordgo.NewState()
	state.Guilds = make([]*discordgo.Guild, count)
	for i := range count {
		state.Guilds[i] = &discordgo.Guild{ID: fmt.Sprintf("guild-%d", i), Name: "Benchmark Guild"}
	}
	return &discordgo.Session{State: state}
}

func TestBulkOverwriteGuildCommandsUsesFixedWorkerPool(t *testing.T) {
	started := make(chan struct{}, 1000)
	release := make(chan struct{})
	var concurrent atomic.Int32
	var maximum atomic.Int32
	b := &Bot{Session: syntheticGuildSession(1000)}
	b.commandBulkOverwriteFn = func(string, string, []*discordgo.ApplicationCommand) ([]*discordgo.ApplicationCommand, error) {
		current := concurrent.Add(1)
		for {
			observed := maximum.Load()
			if current <= observed || maximum.CompareAndSwap(observed, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
		concurrent.Add(-1)
		return nil, nil
	}

	done := make(chan error, 1)
	go func() {
		done <- b.bulkOverwriteGuildCommands("app", nil)
	}()
	for range commandRegistrationWorkers {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("worker pool did not fill")
		}
	}
	select {
	case <-started:
		t.Fatal("more than four registration workers started")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("bulkOverwriteGuildCommands() error = %v", err)
	}
	if got := maximum.Load(); got != commandRegistrationWorkers {
		t.Fatalf("maximum concurrent workers = %d, want %d", got, commandRegistrationWorkers)
	}
}

func BenchmarkBulkOverwriteThousandGuilds(b *testing.B) {
	bot := &Bot{Session: syntheticGuildSession(1000)}
	bot.commandBulkOverwriteFn = func(string, string, []*discordgo.ApplicationCommand) ([]*discordgo.ApplicationCommand, error) {
		return nil, nil
	}
	b.ReportAllocs()
	for b.Loop() {
		if err := bot.bulkOverwriteGuildCommands("app", nil); err != nil {
			b.Fatal(err)
		}
	}
}
