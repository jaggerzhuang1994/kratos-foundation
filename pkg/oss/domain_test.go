package oss

import (
	"strings"
	"testing"
)

func TestBucketDomainHelperRoundTripsBasePathAndReservedCharacters(t *testing.T) {
	helper, err := NewBucketDomainHelper("  https://cdn.example.com/assets/v1///  ")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		key     string
		wantKey string
		wantURL string
	}{
		{
			name:    "plain relative key",
			key:     "images/logo.png",
			wantKey: "images/logo.png",
			wantURL: "https://cdn.example.com/assets/v1/images/logo.png",
		},
		{
			name:    "leading slash and whitespace",
			key:     "  /reports/annual report.pdf  ",
			wantKey: "reports/annual report.pdf",
			wantURL: "https://cdn.example.com/assets/v1/reports/annual%20report.pdf",
		},
		{
			name:    "query and fragment characters in object key",
			key:     "documents/draft?version=2#notes",
			wantKey: "documents/draft?version=2#notes",
			wantURL: "https://cdn.example.com/assets/v1/documents/draft%3Fversion=2%23notes",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fullURL, err := helper.GetFullURL(test.key)
			if err != nil {
				t.Fatal(err)
			}
			if fullURL != test.wantURL {
				t.Fatalf("GetFullURL(%q) = %q, want %q", test.key, fullURL, test.wantURL)
			}
			key, err := helper.ParseObjectKey(fullURL)
			if err != nil {
				t.Fatal(err)
			}
			if key != test.wantKey {
				t.Fatalf("ParseObjectKey(%q) = %q, want %q", fullURL, key, test.wantKey)
			}
		})
	}
}

func TestNewBucketDomainHelperRejectsAmbiguousOrNonHTTPBase(t *testing.T) {
	tests := []struct {
		name   string
		domain string
		want   string
	}{
		{name: "empty", domain: "", want: "absolute HTTP(S)"},
		{name: "relative", domain: "cdn.example.com/assets", want: "absolute HTTP(S)"},
		{name: "unsupported scheme", domain: "ftp://cdn.example.com/assets", want: "absolute HTTP(S)"},
		{name: "missing host", domain: "https:///assets", want: "absolute HTTP(S)"},
		{name: "userinfo", domain: "https://alice@cdn.example.com/assets", want: "userinfo"},
		{name: "query", domain: "https://cdn.example.com/assets?version=2", want: "query"},
		{name: "fragment", domain: "https://cdn.example.com/assets#current", want: "fragment"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			helper, err := NewBucketDomainHelper(test.domain)
			if err == nil || helper != nil {
				t.Fatalf("NewBucketDomainHelper(%q) = (%v, %v), want error", test.domain, helper, err)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %q, want it to contain %q", err, test.want)
			}
		})
	}
}

func TestBucketDomainHelperRejectsForeignOrAmbiguousAbsoluteObjectURL(t *testing.T) {
	helper, err := NewBucketDomainHelper("https://cdn.example.com/assets/v1")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "different host",
			value: "https://other.example.com/assets/v1/logo.png",
			want:  "configured domain",
		},
		{
			name:  "different scheme",
			value: "http://cdn.example.com/assets/v1/logo.png",
			want:  "configured domain",
		},
		{
			name:  "base path prefix collision",
			value: "https://cdn.example.com/assets/v10/logo.png",
			want:  "outside the configured base path",
		},
		{
			name:  "parent base path",
			value: "https://cdn.example.com/assets/logo.png",
			want:  "outside the configured base path",
		},
		{
			name:  "userinfo",
			value: "https://alice@cdn.example.com/assets/v1/logo.png",
			want:  "userinfo",
		},
		{
			name:  "query",
			value: "https://cdn.example.com/assets/v1/logo.png?download=1",
			want:  "query",
		},
		{
			name:  "fragment",
			value: "https://cdn.example.com/assets/v1/logo.png#preview",
			want:  "fragment",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key, err := helper.ParseObjectKey(test.value)
			if err == nil || key != "" {
				t.Fatalf("ParseObjectKey(%q) = (%q, %v), want error", test.value, key, err)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %q, want it to contain %q", err, test.want)
			}
		})
	}
}

func TestBucketDomainHelperRejectsEmptyObjectKey(t *testing.T) {
	helper, err := NewBucketDomainHelper("https://cdn.example.com/assets")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "  /  ", "https://cdn.example.com/assets"} {
		if fullURL, err := helper.GetFullURL(value); err == nil || fullURL != "" {
			t.Errorf("GetFullURL(%q) = (%q, %v), want empty-key error", value, fullURL, err)
		}
	}
}

func TestBucketDomainHelperPreservesLiteralPercentKeys(t *testing.T) {
	helper, err := NewBucketDomainHelper("https://cdn.example.com/assets")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ key, escaped string }{
		{"reports/100%.pdf", "reports/100%25.pdf"},
		{"reports/%2F.pdf", "reports/%252F.pdf"},
		{"reports/%?#.pdf", "reports/%25%3F%23.pdf"},
		{"reports/%zz.pdf", "reports/%25zz.pdf"},
	} {
		t.Run(test.key, func(t *testing.T) {
			if got, err := helper.ParseObjectKey(test.key); err != nil || got != test.key {
				t.Fatalf("ParseObjectKey = %q, %v", got, err)
			}
			full, err := helper.GetFullURL(test.key)
			if err != nil {
				t.Fatal(err)
			}
			if want := "https://cdn.example.com/assets/" + test.escaped; full != want {
				t.Fatalf("GetFullURL = %q, want %q", full, want)
			}
			if got, err := helper.ParseObjectKey(full); err != nil || got != test.key {
				t.Fatalf("round trip = %q, %v", got, err)
			}
		})
	}
	if got, err := helper.ParseObjectKey("https://cdn.example.com/assets/reports%2Fannual.pdf"); err != nil || got != "reports/annual.pdf" {
		t.Fatalf("encoded slash = %q, %v", got, err)
	}
	for _, value := range []string{"https://cdn.example.com/assets/100%.pdf", "ftp://cdn.example.com/assets/file", "https://foreign.example/assets/file", "://cdn.example.com/assets/file"} {
		if _, err := helper.ParseObjectKey(value); err == nil {
			t.Fatalf("invalid URL %q accepted", value)
		}
	}
}
