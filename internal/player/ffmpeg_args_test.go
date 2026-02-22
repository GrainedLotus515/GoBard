package player

import (
	"slices"
	"testing"
	"time"
)

func TestBuildFileFFmpegArgsIncludesSeekOffset(t *testing.T) {
	args := buildFileFFmpegArgs("input.webm", 48000, 2, 90*time.Second+1500*time.Millisecond)

	wantPrefix := []string{"-ss", "00:01:31.500", "-i", "input.webm"}
	if len(args) < len(wantPrefix) || !slices.Equal(args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("buildFileFFmpegArgs() prefix = %v, want prefix %v", args, wantPrefix)
	}
}

func TestBuildFileFFmpegArgsWithoutSeekOffset(t *testing.T) {
	args := buildFileFFmpegArgs("input.webm", 48000, 2, 0)
	if len(args) == 0 || args[0] != "-i" {
		t.Fatalf("buildFileFFmpegArgs() first arg = %q, want -i", firstArg(args))
	}
}

func TestBuildStreamingFFmpegArgsIncludesSeekOffset(t *testing.T) {
	args := buildStreamingFFmpegArgs(48000, 2, 45*time.Second+10*time.Millisecond)

	want := []string{"-ss", "00:00:45.010"}
	if !containsSubsequence(args, want) {
		t.Fatalf("buildStreamingFFmpegArgs() args = %v, expected subsequence %v", args, want)
	}
}

func TestFormatFFmpegTimestamp(t *testing.T) {
	got := formatFFmpegTimestamp(2*time.Hour + 3*time.Minute + 4*time.Second + 567*time.Millisecond)
	if got != "02:03:04.567" {
		t.Fatalf("formatFFmpegTimestamp() = %q, want %q", got, "02:03:04.567")
	}
}

func containsSubsequence(haystack, needle []string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if slices.Equal(haystack[i:i+len(needle)], needle) {
			return true
		}
	}
	return false
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}
