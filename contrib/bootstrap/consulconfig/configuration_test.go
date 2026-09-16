package consulconfig

import (
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
)

func TestNewConfigSources(t *testing.T) {
	info := appinfo.New("test")
	sources := NewConfigSources(info, "local.yaml", "services", NewDefaultLocalConfigPathsProvider(), NewDefaultRemoteConfigPathsProvider())
	if sources.AppInfo != info || sources.LocalPath != "local.yaml" || sources.RemoteDir != "services" || sources.LocalPaths == nil || sources.RemotePaths == nil || sources.LocalSource == nil || sources.RemoteSource == nil {
		t.Fatal("incomplete configuration description")
	}
}
