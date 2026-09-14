package bootstrap_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/config/text"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/bootstrap"
)

func TestLocalConfigSources(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("value: local"), 0600); err != nil {
		t.Fatal(err)
	}
	sources, err := bootstrap.NewLocalConfigSources(bootstrap.LocalConfigPath(path))
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources=%v err=%v", sources, err)
	}
	for _, path := range []string{"", t.TempDir()} {
		if _, err := bootstrap.NewLocalConfigSources(bootstrap.LocalConfigPath(path)); err == nil {
			t.Fatalf("accepted empty local config %q", path)
		}
	}
	t.Setenv("APP_ENV", "prod")
	if sources, err := bootstrap.NewLocalConfigSources("/does/not/exist"); err != nil || len(sources) != 0 {
		t.Fatalf("unused local=%v %v", sources, err)
	}
}

func TestRemoteConfigPaths(t *testing.T) {
	t.Setenv("APP_ENV", "prod")
	got, err := bootstrap.NewRemoteConfigPaths("orders")
	want := bootstrap.RemoteConfigPaths{"configs/common.yaml", "configs/prod/common.yaml", "secrets/common.yaml", "secrets/prod/common.yaml", "configs/orders/*.yaml", "configs/orders/prod/*.yaml", "secrets/orders/*.yaml", "secrets/orders/prod/*.yaml"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("paths=%v err=%v", got, err)
	}
	for _, name := range []bootstrap.RemoteConfigDirName{"", ".", "..", "a/b", "a\\b", "a*", " name"} {
		if _, err := bootstrap.NewRemoteConfigPaths(name); err == nil {
			t.Fatalf("accepted %q", name)
		}
	}
	info := appinfo.New("test")
	if got := bootstrap.DefaultAppRemoteConfigDirNameProvider(info); string(got) != info.Name() {
		t.Fatalf("default=%s", got)
	}
}

func TestConfigSourcesSelectWithoutMixing(t *testing.T) {
	local, err := text.NewSource("local.yaml", "yaml", "value: local")
	if err != nil {
		t.Fatal(err)
	}
	remote, err := text.NewSource("remote.yaml", "yaml", "value: remote")
	if err != nil {
		t.Fatal(err)
	}
	for _, environment := range []string{"local", "dev", "test", "pre", "prod"} {
		t.Run(environment, func(t *testing.T) {
			t.Setenv("APP_ENV", environment)
			files := bootstrap.LocalConfigSources{local}
			remotes := bootstrap.RemoteConfigSources{remote}
			sources := bootstrap.NewConfigSources(files, remotes)
			want := remote
			if environment == "local" {
				want = local
			}
			if len(sources) != 1 || sources[0] != want {
				t.Fatal("incorrect source selection")
			}
			sources[0] = nil
			if files[0] != local || remotes[0] != remote {
				t.Fatal("source slice aliases input")
			}
		})
	}
}
