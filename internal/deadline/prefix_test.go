package deadline

import (
	"strings"
	"testing"
	"time"
)

func checkPrefixLookup(t *testing.T, prefixes []string, operation string) {
	t.Helper()
	routes := make([]routePolicy, 0, len(prefixes))
	seen := make(map[string]bool)
	var want policy
	found := false
	longest := 0
	for _, prefix := range prefixes {
		if prefix == "" || seen[prefix] {
			continue
		}
		seen[prefix] = true
		value := policy{fallbackTimeout: time.Duration(len(routes) + 1)}
		routes = append(routes, routePolicy{prefix: prefix, policy: value})
		if operation == "" {
			if !found {
				want, found = value, true
			}
			continue
		}
		if len(prefix) > longest && strings.HasPrefix(operation, prefix) {
			want, found, longest = value, true, len(prefix)
		}
	}
	index := newPrefixIndex(routes)
	got, ok := index.lookup(operation)
	if got != want || ok != found {
		t.Fatalf("prefixes=%q operation=%q got=(%+v,%v) want=(%+v,%v)", prefixes, operation, got, ok, want, found)
	}
}

func TestPrefixIndexMatchesLinearLookup(t *testing.T) {
	for _, prefixes := range [][]string{nil, {"/only"}, {"/z", "/a/b", "/a", "/a/b/c", "/a/bc", "订单/", "订单/详情", "\xff", "\xff\x80", "abc", "abd"}} {
		for _, operation := range []string{"", "/only", "/only/child", "/a", "/a/b", "/a/b/c/d", "/a/bc", "/a/bd", "/unknown", "ab", "abc", "abd", "订单/详情/123", "\xff\x80\x01"} {
			checkPrefixLookup(t, prefixes, operation)
		}
	}
}

func FuzzPrefixIndexMatchesLinearLookup(f *testing.F) {
	f.Add("/z|/a/b|/a|/a/b/c", "/a/b/c/d")
	f.Add("订单/|订单/详情|/root", "订单/详情/123")
	f.Add("b|a", "")
	f.Fuzz(func(t *testing.T, rules, operation string) {
		if len(rules) > 2048 || len(operation) > 2048 {
			t.Skip()
		}
		checkPrefixLookup(t, strings.Split(rules, "|"), operation)
	})
}
