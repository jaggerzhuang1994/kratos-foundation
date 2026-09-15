package file

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
)

func TestAddConfigSourceDefersIOAndCopiesPaths(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	paths := []string{path}
	loader := AddConfigSource(paths...)
	paths[0] = "changed"
	if err := os.WriteFile(path, []byte("test: loaded"), 0600); err != nil {
		t.Fatal(err)
	}
	sources, err := loader()
	if err != nil {
		t.Fatal(err)
	}
	manager, cleanup, err := config.NewManager(sources)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var value string
	if err := manager.Load("test", &value); err != nil || value != "loaded" {
		t.Fatalf("%q %v", value, err)
	}
	if _, err := AddConfigSource("[")(); err == nil {
		t.Fatal("invalid glob accepted")
	}
	if sources, err := AddConfigSource()(); err != nil || len(sources) != 0 {
		t.Fatalf("%v %v", sources, err)
	}
}
