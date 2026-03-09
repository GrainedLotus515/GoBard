package voiceconn

import "context"

// Connection defines the minimal voice operations the player needs.
type Connection interface {
	SetSpeaking(ctx context.Context, speaking bool) error
	SendOpusFrame(frame []byte) error
	Disconnect(ctx context.Context) error
}
