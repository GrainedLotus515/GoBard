package architecture

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// Every explicit package named by a Docker quality stage must survive package
// moves/removal. This catches stale benchmark gates during ordinary docker-test.
func TestDockerQualityPackageTargetsExist(t *testing.T) {
	data, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(`\./(?:internal|cmd)/[a-zA-Z0-9_/-]+`)
	for _, target := range pattern.FindAllString(string(data), -1) {
		files, err := filepath.Glob(filepath.Join("../..", target, "*.go"))
		if err != nil || len(files) == 0 {
			t.Errorf("Docker target %s has no Go source: %v", target, err)
		}
	}
}
