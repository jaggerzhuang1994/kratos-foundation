package consulconfig

import (
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
)

func TestNewConfigSources(t *testing.T) {
	info := appinfo.New("test")
	sources := NewConfigSources(info, "local.yaml", "services", "shared-orders", NewDefaultLocalConfigPathsProvider(), NewDefaultRemoteConfigPathsProvider())
	if sources.AppInfo != info || sources.LocalPath != "local.yaml" || sources.RemoteDir != "services" || sources.RemoteName != "shared-orders" || sources.LocalPaths == nil || sources.RemotePaths == nil || sources.LocalSource == nil || sources.RemoteSource == nil {
		t.Fatal("incomplete configuration description")
	}
}

func TestNewDefaultRemoteConfigName(t *testing.T) {
	info := appinfo.New("test")
	if got := NewDefaultRemoteConfigName(info); string(got) != info.Name() {
		t.Fatalf("name=%q want=%q", got, info.Name())
	}
}
