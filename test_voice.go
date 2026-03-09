package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/GrainedLotus515/gobard/internal/discordvoice"
	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)

func main() {
	// Load .env
	godotenv.Load()

	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		log.Fatal("DISCORD_TOKEN not set")
	}

	// Create session
	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Fatal("Error creating Discord session:", err)
	}

	var voiceManager *discordvoice.Manager

	dg.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Printf("Logged in as %s", r.User.Username)
	})

	dg.AddHandler(func(_ *discordgo.Session, update *discordgo.VoiceStateUpdate) {
		if voiceManager != nil {
			voiceManager.HandleVoiceStateUpdate(update)
		}
	})

	dg.AddHandler(func(_ *discordgo.Session, update *discordgo.VoiceServerUpdate) {
		if voiceManager != nil {
			voiceManager.HandleVoiceServerUpdate(update)
		}
	})

	dg.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if m.Content == "!testvoice" {
			// Find user's voice channel
			guild, err := s.State.Guild(m.GuildID)
			if err != nil {
				s.ChannelMessageSend(m.ChannelID, "Error: "+err.Error())
				return
			}

			var channelID string
			for _, vs := range guild.VoiceStates {
				if vs.UserID == m.Author.ID {
					channelID = vs.ChannelID
					break
				}
			}

			if channelID == "" {
				s.ChannelMessageSend(m.ChannelID, "You need to be in a voice channel!")
				return
			}

			s.ChannelMessageSend(m.ChannelID, "Attempting to join voice...")
			log.Printf("Joining voice channel %s", channelID)

			if voiceManager == nil {
				s.ChannelMessageSend(m.ChannelID, "Voice manager is not initialized yet")
				return
			}

			// Try to join with context
			ctx := context.Background()
			vc, err := voiceManager.Join(ctx, m.GuildID, channelID, false, false)
			if err != nil {
				log.Printf("Error joining voice: %v", err)
				s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("❌ Failed to join: %v", err))
				return
			}

			log.Printf("Successfully joined voice channel!")
			s.ChannelMessageSend(m.ChannelID, "✅ Successfully joined voice channel! Will disconnect in 5 seconds...")

			// Wait a bit
			time.Sleep(5 * time.Second)

			// Disconnect with context
			vc.Disconnect(ctx)
			s.ChannelMessageSend(m.ChannelID, "Disconnected from voice")
		}
	})

	dg.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsGuildVoiceStates | discordgo.IntentsMessageContent

	err = dg.Open()
	if err != nil {
		log.Fatal("Error opening connection:", err)
	}
	defer dg.Close()

	voiceManager, err = discordvoice.NewManager(dg)
	if err != nil {
		log.Fatal("Error creating voice manager:", err)
	}

	log.Println("Bot is running. Type !testvoice in a channel while in a voice channel to test.")
	log.Println("Press CTRL-C to exit.")

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc
}
