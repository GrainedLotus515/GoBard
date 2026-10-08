package failure

import (
	"errors"
	"strings"
	"testing"
)

func TestBoundedSafeDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		text string
		want Category
	}{
		{"Sign in to confirm you're not a bot", Authentication},
		{"HTTP Error 429", Throttled},
		{"Private video", Unavailable},
		{"unknown secret token", Source},
	} {
		var d Diagnostic
		input := tc.text + strings.Repeat("https://secret.invalid/token ", 10000)
		n, err := d.Write([]byte(input))
		if err != nil || n != len(input) || d.n != len(d.data) {
			t.Fatal(n, err, d.n)
		}
		cause := errors.New("private credential")
		f := d.Failure("extract", Source, cause)
		if f.Category != tc.want || len(f.Fingerprint) != 16 || !errors.Is(f, cause) {
			t.Fatalf("%+v", f)
		}
		if strings.Contains(f.Error(), "secret") || strings.Contains(f.Error(), "credential") {
			t.Fatal(f)
		}
	}
}
