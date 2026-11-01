package sourceurl

import "testing"

func TestClassifyYouTubeURLRejectsDeceptiveAndNonHTTPSInputs(t *testing.T) {
	validVideo := "https://www.youtube.com/watch?v=abc123XYZ89"
	validPlaylist := "https://music.youtube.com/playlist?list=PL_abc-123"

	tests := []struct {
		name     string
		raw      string
		wantKind URLKind
		wantURL  string
		wantErr  bool
	}{
		{"valid video", validVideo, URLKindVideo, validVideo, false},
		{"valid playlist", validPlaylist, URLKindPlaylist, "https://www.youtube.com/playlist?list=PL_abc-123", false},
		{"http is rejected", "http://www.youtube.com/watch?v=abc123XYZ89", URLKindUnknown, "", true},
		{"suffix host is rejected", "https://youtube.com.evil.test/watch?v=abc123XYZ89", URLKindUnknown, "", true},
		{"path substring is rejected", "https://127.0.0.1/youtube.com/watch?v=abc123XYZ89", URLKindUnknown, "", true},
		{"userinfo is rejected", "https://www.youtube.com@127.0.0.1/watch?v=abc123XYZ89", URLKindUnknown, "", true},
		{"scheme relative is rejected", "//www.youtube.com/watch?v=abc123XYZ89", URLKindUnknown, "", true},
		{"unsupported shorts path", "https://www.youtube.com/shorts/abc123XYZ89", URLKindUnknown, "", true},
		{"invalid video identifier", "https://www.youtube.com/watch?v=abc%2Fdef", URLKindUnknown, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotURL, gotKind, err := ClassifyYouTubeURL(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ClassifyYouTubeURL(%q) error = %v, wantErr %v", tt.raw, err, tt.wantErr)
			}
			if gotKind != tt.wantKind {
				t.Fatalf("ClassifyYouTubeURL(%q) kind = %v, want %v", tt.raw, gotKind, tt.wantKind)
			}
			if gotURL != tt.wantURL {
				t.Fatalf("ClassifyYouTubeURL(%q) URL = %q, want %q", tt.raw, gotURL, tt.wantURL)
			}
		})
	}
}
