package appinfo

import (
	"strings"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/env"
)

func TestNewCreatesStableIdentityAndDefensiveMetadata(t *testing.T) {
	t.Setenv("APP_ENV", env.Dev)
	value := New("v1.2.3")
	metadata := value.Metadata()

	if value.Version() != "v1.2.3" || value.Name() == "" || value.ID() == "" {
		t.Fatalf("incomplete app identity: %#v", value)
	}
	if metadata[MetadataEnvironment] != env.Dev || metadata[MetadataHostname] == "" {
		t.Fatalf("Metadata() = %#v, want environment and hostname", metadata)
	}
	if !strings.HasPrefix(value.ID(), metadata[MetadataHostname]+"-") {
		t.Fatalf("ID() = %q, want hostname prefix %q", value.ID(), metadata[MetadataHostname]+"-")
	}

	metadata[MetadataEnvironment] = env.Prod
	t.Setenv("APP_ENV", env.Prod)
	if got := value.Metadata()[MetadataEnvironment]; got != env.Dev {
		t.Fatalf("Metadata() environment changed to %q, want construction snapshot %q", got, env.Dev)
	}
}

func TestSnapshotsAreNonEmpty(t *testing.T) {
	if processHostname == "" {
		t.Fatal("Hostname returned empty string")
	}
	if processExecutableName == "" {
		t.Fatal("ExecutableName returned empty string")
	}
}
