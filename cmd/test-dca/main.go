package main

import (
	"io"
	"log"
	"time"

	"github.com/jonas747/dca"
)

func main() {
	source := "cache/810f032aadd125db6d1785c9d709f63c.webm"
	
	// Try with AudioFilter explicitly empty to override defaults
	options := &dca.EncodeOptions{
		Volume:           0,  // 0 means no volume adjustment
		Channels:         2,
		FrameRate:        48000,
		FrameDuration:    20,
		Bitrate:          128,
		Application:      "audio",
		RawOutput:        false,
		AudioFilter:      "", // Explicitly empty - no filters
	}
	
	log.Printf("Starting DCA encoding of: %s", source)
	encoder, err := dca.EncodeFile(source, options)
	if err != nil {
		log.Fatalf("ERROR encoding: %v", err)
	}
	defer encoder.Cleanup()
	
	log.Printf("DCA encoding started successfully")
	
	// Give it a moment to start
	time.Sleep(500 * time.Millisecond)
	
	log.Printf("Encoder running: %v", encoder.Running())
	log.Printf("Encoder error: %v", encoder.Error())
	if !encoder.Running() {
		log.Printf("FFmpeg messages:\n%s", encoder.FFMPEGMessages())
	}
	
	frameCount := 0
	for {
		_, err := encoder.OpusFrame()
		if err != nil {
			if err != io.EOF {
				log.Printf("Error reading frame: %v", err)
			}
			break
		}
		frameCount++
		if frameCount % 100 == 0 {
			log.Printf("Got %d frames...", frameCount)
		}
	}
	
	log.Printf("Total frames: %d", frameCount)
}
