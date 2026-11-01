package architecture

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDiscordSDKIsolation(t *testing.T) {
	for _, root := range []string{"..", "../../cmd"} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, spec := range file.Imports {
				imported, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				if !strings.HasPrefix(imported, "github.com/bwmarrin/discordgo") && !strings.HasPrefix(imported, "github.com/disgoorg/") {
					continue
				}
				adapter := strings.HasPrefix(path, "../bot/") || strings.HasPrefix(path, "../botui/") || strings.HasPrefix(path, "../discordvoice/")
				if !adapter {
					t.Errorf("Discord SDK import outside adapter boundary: %s imports %s", path, imported)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
