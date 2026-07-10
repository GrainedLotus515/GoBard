package sourceurl

import "testing"

func TestValidateCanonicalYouTubeVideoURL(t *testing.T) {
	valid := "https://www.youtube.com/watch?v=abc123_XY-9"
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "canonical", raw: valid, want: true},
		{name: "short URL is not a playback boundary URL", raw: "https://youtu.be/abc123_XY-9", want: false},
		{name: "extra query parameter", raw: valid + "&t=1", want: false},
		{name: "non canonical host casing", raw: "https://WWW.youtube.com/watch?v=abc123_XY-9", want: false},
		{name: "private host", raw: "https://127.0.0.1/watch?v=abc123_XY-9", want: false},
		{name: "invalid identifier", raw: "https://www.youtube.com/watch?v=abc%2Fdef", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateCanonicalYouTubeVideoURL(tt.raw)
			if (err == nil) != tt.want {
				t.Fatalf("ValidateCanonicalYouTubeVideoURL(%q) error = %v, want valid %v", tt.raw, err, tt.want)
			}
			if tt.want && got != tt.raw {
				t.Fatalf("ValidateCanonicalYouTubeVideoURL() = %q, want %q", got, tt.raw)
			}
		})
	}
}

func TestIsPublicHTTPURL(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{raw: "https://media.example/audio.webm", want: true},
		{raw: "https://127.0.0.1/audio.webm", want: false},
		{raw: "https://[::1]/audio.webm", want: false},
		{raw: "https://169.254.169.254/latest/meta-data", want: false},
		{raw: "https://127.1/audio.webm", want: false},
		{raw: "https://0177.0.0.1/audio.webm", want: false},
		{raw: "https://0x7f.0.0.1/audio.webm", want: false},
		{raw: "https://0x7f000001/audio.webm", want: false},
		{raw: "https://[fe80::1%25lo]/audio.webm", want: false},
		{raw: "https://localhost/audio.webm", want: false},
		{raw: "file:///etc/passwd", want: false},
	}
	for _, tt := range tests {
		if got := IsPublicHTTPURL(tt.raw); got != tt.want {
			t.Fatalf("IsPublicHTTPURL(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}
