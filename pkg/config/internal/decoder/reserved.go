package decoder

import (
	"errors"
	"fmt"
	"slices"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// ErrRemovedField 表示配置仍在使用已删除、被协议保留的字段。
var ErrRemovedField = errors.New("config field has been removed")

// ValidateReserved 检查已知消息中的保留字段，允许无关业务字段存在。
// 只报告路径，不包含配置值，避免诊断泄漏凭据。
func ValidateReserved(value any, descriptor protoreflect.MessageDescriptor, path string) error {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		fieldPath := key
		if path != "" {
			fieldPath = path + "." + key
		}
		name := normalizedKey(key)
		reserved := descriptor.ReservedNames()
		for index := 0; index < reserved.Len(); index++ {
			if name == normalizedKey(string(reserved.Get(index))) {
				return fmt.Errorf("%w: %s; remove the field and migrate its configuration (see pkg/config/README.md)", ErrRemovedField, fieldPath)
			}
		}
		fields := descriptor.Fields()
		for index := 0; index < fields.Len(); index++ {
			field := fields.Get(index)
			if normalizedKey(string(field.Name())) != name {
				continue
			}
			if err := validateReservedValue(object[key], field, fieldPath); err != nil {
				return err
			}
			break
		}
	}
	return nil
}

func validateReservedValue(value any, field protoreflect.FieldDescriptor, path string) error {
	if field.IsMap() {
		if field.MapValue().Message() == nil {
			return nil
		}
		entries, _ := value.(map[string]any)
		keys := make([]string, 0, len(entries))
		for key := range entries {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			if err := ValidateReserved(entries[key], field.MapValue().Message(), path+"."+key); err != nil {
				return err
			}
		}
		return nil
	}
	if field.Message() == nil {
		return nil
	}
	if field.IsList() {
		entries, _ := value.([]any)
		for index, entry := range entries {
			if err := ValidateReserved(entry, field.Message(), fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
		return nil
	}
	return ValidateReserved(value, field.Message(), path)
}
