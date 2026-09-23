package consulconfig

import (
	"slices"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
)

func TestNewConfigSources(t *testing.T) {
	info := appinfo.New("test")
	local, remote := []string{"local/{{app}}.yaml"}, []string{"configs/{{env}}/{{app}}.yaml"}
	sources := NewConfigSources(info, local, remote)
	if sources.AppInfo != info || !slices.Equal(sources.LocalPaths, local) || !slices.Equal(sources.RemotePaths, remote) || sources.LocalSource == nil || sources.RemoteSource == nil {
		t.Fatal("incomplete configuration description")
	}
}
