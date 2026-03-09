package bot

import (
	"fmt"
	"strings"
)

func wrapVoiceJoinError(err error) error {
	if err == nil {
		return nil
	}

	message := strings.ToLower(err.Error())
	if strings.Contains(message, "close 4017") || strings.Contains(message, "e2ee/dave protocol required") {
		return fmt.Errorf(
			"failed to join voice channel: Discord rejected the voice connection with close code 4017 because E2EE/DAVE is required. Ensure the DAVE-capable voice stack is active and the native libdave dependency is installed correctly: %w",
			err,
		)
	}

	return fmt.Errorf("failed to join voice channel: %w", err)
}
