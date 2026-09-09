package snapshot

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var placeholderRegexp = regexp.MustCompile(`\$\{(.*?)}`)

type resolveState uint8

const (
	resolving resolveState = iota + 1
	resolved
)

type resolver struct {
	values map[string]any
	states map[string]resolveState
	cache  map[string]any
	stack  []string
}

func resolve(values map[string]any) (map[string]any, error) {
	valueResolver := &resolver{
		values: values,
		states: make(map[string]resolveState),
		cache:  make(map[string]any),
	}
	resolvedValue, err := valueResolver.resolveValue(values, "")
	if err != nil {
		return nil, err
	}
	tree, _ := resolvedValue.(map[string]any)
	return tree, nil
}

func (r *resolver) resolvePath(path string) (any, error) {
	switch r.states[path] {
	case resolved:
		return r.cache[path], nil
	case resolving:
		return nil, fmt.Errorf("config placeholder cycle: %s", strings.Join(r.cycle(path), " -> "))
	}

	value, found := lookup(r.values, path)
	if !found {
		return nil, fmt.Errorf("config reference %q is missing", path)
	}
	r.states[path] = resolving
	r.stack = append(r.stack, path)
	resolvedValue, err := r.resolveValue(value, path)
	r.stack = r.stack[:len(r.stack)-1]
	if err != nil {
		delete(r.states, path)
		return nil, err
	}
	r.states[path] = resolved
	r.cache[path] = resolvedValue
	return resolvedValue, nil
}

func (r *resolver) resolveValue(value any, location string) (any, error) {
	switch typed := value.(type) {
	case string:
		return r.expand(typed, location)
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		result := make(map[string]any, len(typed))
		for _, key := range keys {
			resolvedValue, err := r.resolveValue(typed[key], joinPath(location, key))
			if err != nil {
				return nil, err
			}
			result[key] = resolvedValue
		}
		return result, nil
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			resolvedValue, err := r.resolveValue(item, fmt.Sprintf("%s[%d]", location, index))
			if err != nil {
				return nil, err
			}
			result[index] = resolvedValue
		}
		return result, nil
	default:
		return value, nil
	}
}

func (r *resolver) expand(value, location string) (string, error) {
	matches := placeholderRegexp.FindAllStringSubmatchIndex(value, -1)
	if len(matches) == 0 {
		return value, nil
	}

	var builder strings.Builder
	last := 0
	for _, match := range matches {
		builder.WriteString(value[last:match[0]])
		path, fallback, hasFallback := strings.Cut(strings.TrimSpace(value[match[2]:match[3]]), ":")
		path = strings.TrimSpace(path)
		if path == "" {
			return "", fmt.Errorf("resolve config placeholder in %q: reference path is empty", location)
		}

		replacement, found, err := r.reference(path)
		if err != nil {
			return "", fmt.Errorf("resolve config placeholder in %q: %w", location, err)
		}
		if !found {
			if !hasFallback {
				return "", fmt.Errorf(
					"resolve config placeholder in %q: config reference %q is missing",
					location,
					path,
				)
			}
			replacement = fallback
		}
		builder.WriteString(replacement)
		last = match[1]
	}
	builder.WriteString(value[last:])
	return builder.String(), nil
}

func (r *resolver) reference(path string) (string, bool, error) {
	raw, found := lookup(r.values, path)
	if !found {
		return "", false, nil
	}
	if _, ok := scalarString(raw); !ok {
		return "", true, fmt.Errorf("config reference %q has non-scalar type %T", path, raw)
	}
	resolvedValue, err := r.resolvePath(path)
	if err != nil {
		return "", true, err
	}
	text, ok := scalarString(resolvedValue)
	if !ok {
		return "", true, fmt.Errorf("config reference %q resolved to non-scalar type %T", path, resolvedValue)
	}
	return text, true, nil
}

func scalarString(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case json.Number, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return fmt.Sprint(typed), true
	default:
		return "", false
	}
}

func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

func (r *resolver) cycle(path string) []string {
	start := 0
	for index, item := range r.stack {
		if item == path {
			start = index
			break
		}
	}
	cycle := append([]string(nil), r.stack[start:]...)
	return append(cycle, path)
}
