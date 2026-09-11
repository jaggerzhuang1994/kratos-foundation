package decoder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	kratosconfig "github.com/go-kratos/kratos/v2/config"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Decoder 固定一次加载的 target 类型和可选默认值。
type Decoder struct {
	targetType  reflect.Type
	defaultTree any
	hasDefault  bool
}

// New 校验 target，并复制可选默认值。
func New(target any, defaultValue []any) (*Decoder, error) {
	if len(defaultValue) > 1 {
		return nil, errors.New("at most one default value is allowed")
	}
	targetType, err := allocatedPointerType(target, "target")
	if err != nil {
		return nil, err
	}
	valueDecoder := &Decoder{targetType: targetType}
	if len(defaultValue) == 0 {
		return valueDecoder, nil
	}
	defaultType, err := allocatedPointerType(defaultValue[0], "default value")
	if err != nil {
		return nil, err
	}
	if defaultType != targetType {
		return nil, fmt.Errorf(
			"default value type %s does not match target type %s",
			defaultType,
			targetType,
		)
	}
	valueDecoder.defaultTree, err = buildTree(defaultValue[0])
	if err != nil {
		return nil, fmt.Errorf("encode default value: %w", err)
	}
	valueDecoder.hasDefault = true
	return valueDecoder, nil
}

// HasDefault 报告调用方是否提供了默认值。
func (d *Decoder) HasDefault() bool {
	return d.hasDefault
}

// NewTarget 为订阅回调分配独立 target。
func (d *Decoder) NewTarget() any {
	target := reflect.New(d.targetType.Elem())
	if target.Type() != d.targetType {
		target = target.Convert(d.targetType)
	}
	return target.Interface()
}

// Apply 清空 target，再按“默认值先行、配置覆盖”解码。
func (d *Decoder) Apply(value any, found bool, target any) error {
	targetType, err := allocatedPointerType(target, "target")
	if err != nil {
		return err
	}
	if targetType != d.targetType {
		return fmt.Errorf(
			"target type %s does not match prototype type %s",
			targetType,
			d.targetType,
		)
	}
	clearPointer(target)
	if !d.hasDefault {
		if !found {
			return kratosconfig.ErrNotFound
		}
		return scan(value, target)
	}

	// applyDefaults 自行复制合并结果；缺失配置时 scan 只读默认树并解码为独立 target。
	merged := d.defaultTree
	if found {
		merged = applyDefaults(value, merged, d.targetType)
	}
	return scan(merged, target)
}

func allocatedPointerType(value any, name string) (reflect.Type, error) {
	if value == nil {
		return nil, fmt.Errorf("%s is nil", name)
	}
	valueOf := reflect.ValueOf(value)
	valueType := valueOf.Type()
	if valueType.Kind() != reflect.Pointer {
		return nil, fmt.Errorf("%s must be a pointer, got %s", name, valueType)
	}
	if valueOf.IsNil() {
		return nil, fmt.Errorf("%s is a nil pointer", name)
	}
	return valueType, nil
}

func clearPointer(target any) {
	value := reflect.ValueOf(target)
	value.Elem().Set(reflect.Zero(value.Elem().Type()))
}

func buildTree(value any) (any, error) {
	var (
		data []byte
		err  error
	)
	if message, ok := value.(proto.Message); ok {
		data, err = (protojson.MarshalOptions{UseProtoNames: true}).Marshal(message)
	} else {
		data, err = json.Marshal(value)
	}
	if err != nil {
		return nil, err
	}
	var tree any
	// 默认值中的整数在合并前保留十进制文本，避免先转 float64 丢失精度。
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&tree); err != nil {
		return nil, err
	}
	return tree, nil
}

func scan(source, target any) error {
	data, err := json.Marshal(source)
	if err != nil {
		return err
	}
	if message, ok := target.(proto.Message); ok {
		if err := ValidateReserved(source, message.ProtoReflect().Descriptor(), ""); err != nil {
			return err
		}
		return protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal(data, message)
	}
	return json.Unmarshal(data, target)
}
