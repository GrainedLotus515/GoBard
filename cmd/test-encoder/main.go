package main

import (
	"io"
	"log"
	"time"

	"github.com/GrainedLotus515/gobard/internal/player"
)

func main() {
	source := "cache/810f032aadd125db6d1785c9d709f63c.webm"

	log.Println("=== Testing Custom Encoder ===")
	log.Printf("Creating encoder for: %s", source)
	encoder, err := player.NewCustomEncoder(source, 48000, 2)
	if err != nil {
		log.Fatalf("Failed to create encoder: %v", err)
	}
	defer encoder.Cleanup()

	log.Println("Encoder created successfully, reading all frames...")
	time.Sleep(500 * time.Millisecond)

	frameCount := 0
	totalBytes := 0
	lastPrint := 0
	for {
		frame, err := encoder.OpusFrame()
		if err != nil {
			if err == io.EOF {
				log.Printf("Reached EOF")
				break
			}
			log.Printf("Error: %v", err)
			break
		}

		frameCount++
		totalBytes += len(frame)

		if frameCount-lastPrint >= 1000 || frameCount <= 10 {
			log.Printf("Frame %d: Got %d bytes (total: %d bytes)", frameCount, len(frame), totalBytes)
			lastPrint = frameCount
		}
	}

	log.Printf("Test complete - Got %d frames, %d total bytes", frameCount, totalBytes)
	log.Printf("Audio duration: ~%.1f seconds (at 48kHz, 20ms frames)", float64(frameCount)*0.020)
}
