package main

import (
	"io"
	"log"
	"os"
	"time"

	"github.com/jonas747/dca"
)

func main() {
	source := "cache/810f032aadd125db6d1785c9d709f63c.webm"

	file, err := os.Open(source)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	options := &dca.EncodeOptions{
		Volume:        0,
		Channels:      2,
		FrameRate:     48000,
		FrameDuration: 20,
		Bitrate:       128,
		Application:   "audio",
		AudioFilter:   "",
	}

	log.Println("Starting EncodeMem...")
	encoder, err := dca.EncodeMem(file, options)
	if err != nil {
		log.Fatalf("EncodeMem failed: %v", err)
	}
	defer encoder.Cleanup()

	time.Sleep(500 * time.Millisecond)
	log.Printf("Encoder running: %v", encoder.Running())
	log.Printf("Encoder error: %v", encoder.Error())

	frameCount := 0
	for i := 0; i < 10; i++ {
		frame, err := encoder.OpusFrame()
		if err != nil {
			log.Printf("Frame %d: error %v", i, err)
			if err == io.EOF {
				break
			}
		} else {
			frameCount++
			log.Printf("Frame %d: got %d bytes", i, len(frame))
		}
	}
	log.Printf("Got %d frames", frameCount)
}
