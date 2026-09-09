package snapshot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
	kratosencoding "github.com/go-kratos/kratos/v2/encoding"
	_ "github.com/go-kratos/kratos/v2/encoding/json"
	_ "github.com/go-kratos/kratos/v2/encoding/yaml"
)

func build(values []*kratosconfig.KeyValue) (map[string]any, error) {
	merged := make(map[string]any)
	for _, value := range values {
		if value == nil {
			continue
		}
		next, err := decode(value)
		if err != nil {
			return nil, fmt.Errorf("decode config %q: %w", value.Key, err)
		}
		merged = merge(merged, normalize(next)).(map[string]any)
	}
	resolved, err := resolve(merged)
	if err != nil {
		return nil, err
	}
	return resolved, nil
}

func decode(source *kratosconfig.KeyValue) (map[string]any, error) {
	target := make(map[string]any)
	if source.Format == "" {
		next := target
		keys := strings.Split(source.Key, ".")
		for index, key := range keys {
			if index == len(keys)-1 {
				next[key] = append([]byte(nil), source.Value...)
				break
			}
			nested := make(map[string]any)
			next[key] = nested
			next = nested
		}
		return target, nil
	}
	if source.Format == "json" {
		// JSON 数字先保留原始文本，直到写入具体 Go/protobuf 类型时再转换。
		decoder := json.NewDecoder(bytes.NewReader(source.Value))
		decoder.UseNumber()
		if err := decoder.Decode(&target); err != nil {
			return nil, err
		}
		// Decode 支持 JSON 流；配置仍只接受一个完整值，禁止忽略尾随内容。
		if err := decoder.Decode(new(any)); err != io.EOF {
			if err == nil {
				return nil, errors.New("config contains multiple JSON values")
			}
			return nil, err
		}
		return target, nil
	}
	codec := kratosencoding.GetCodec(source.Format)
	if codec == nil {
		return nil, fmt.Errorf("unsupported key: %s format: %s", source.Key, source.Format)
	}
	if err := codec.Unmarshal(source.Value, &target); err != nil {
		return nil, err
	}
	return target, nil
}

func normalize(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = normalize(item)
		}
		return result
	case map[any]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[fmt.Sprint(key)] = normalize(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = normalize(item)
		}
		return result
	case []byte:
		return string(typed)
	default:
		return value
	}
}
