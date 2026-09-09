package gormscope

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// ErrInvalidCursor indicates a malformed or incompatible cursor.
var ErrInvalidCursor = errors.New("invalid GORM cursor")

var schemaCache sync.Map

// SortKeys defines a deterministic lexicographic cursor order. Include a
// unique final key (usually the primary key) to avoid skipped duplicate rows.
type SortKeys []SortKey

// SortKey describes one cursor column and its direction/value encoding.
type SortKey interface {
	field() string
	ascending() bool
	encode(any) (string, error)
	decode(string) (any, error)
}

// GetSortKey 从模型提取字段值并用 sep 连接为游标。
func (keys SortKeys) GetSortKey(model any, sep string) (string, error) {
	if err := validateSortKeys(keys, sep); err != nil {
		return "", err
	}
	if model == nil {
		return "", fmt.Errorf("%w: model is nil", ErrInvalidCursor)
	}
	modelSchema, err := schema.Parse(model, &schemaCache, schema.NamingStrategy{})
	if err != nil {
		return "", fmt.Errorf("parse cursor model: %w", err)
	}
	modelValue := reflect.ValueOf(model)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		fieldName := key.field()
		if separator := strings.LastIndexByte(fieldName, '.'); separator >= 0 {
			fieldName = fieldName[separator+1:]
		}
		field := modelSchema.LookUpField(fieldName)
		if field == nil {
			return "", fmt.Errorf("%w: model field %q not found", ErrInvalidCursor, key.field())
		}
		value, _ := field.ValueOf(context.Background(), modelValue)
		encoded, encodeErr := key.encode(value)
		if encodeErr != nil {
			return "", fmt.Errorf("%w: encode %q: %v", ErrInvalidCursor, key.field(), encodeErr)
		}
		if strings.Contains(encoded, sep) {
			return "", fmt.Errorf("%w: value for %q contains separator", ErrInvalidCursor, key.field())
		}
		values = append(values, encoded)
	}
	return strings.Join(values, sep), nil
}

// ScopeFromSortKey 应用确定性排序，并按可选游标添加字典序查找条件。
func ScopeFromSortKey(cursor *string, keys SortKeys, sep string) Scope {
	if err := validateSortKeys(keys, sep); err != nil {
		return func(db *gorm.DB) *gorm.DB { return addError(db, err) }
	}
	return func(db *gorm.DB) *gorm.DB {
		if cursor != nil && *cursor != "" {
			values := strings.Split(*cursor, sep)
			if len(values) != len(keys) {
				return addError(db, fmt.Errorf("%w: expected %d values", ErrInvalidCursor, len(keys)))
			}
			query, arguments, err := cursorQuery(db, keys, values)
			if err != nil {
				return addError(db, err)
			}
			db = db.Where(query, arguments...)
		}
		for _, key := range keys {
			column, err := clauseColumn(key.field())
			if err != nil {
				return addError(db, err)
			}
			db = db.Order(clause.OrderByColumn{Column: column, Desc: !key.ascending()})
		}
		return db
	}
}

// cursorQuery 递归构造复合排序键的字典序查询。
func cursorQuery(db *gorm.DB, keys SortKeys, values []string) (string, []any, error) {
	key := keys[0]
	value, err := key.decode(values[0])
	if err != nil {
		return "", nil, fmt.Errorf("%w: decode %q: %v", ErrInvalidCursor, key.field(), err)
	}
	quoted, err := QuoteIdentifier(db, key.field())
	if err != nil {
		return "", nil, err
	}
	operator := ">"
	if !key.ascending() {
		operator = "<"
	}
	if len(keys) == 1 {
		return quoted + " " + operator + " ?", []any{value}, nil
	}
	nextQuery, nextArguments, err := cursorQuery(db, keys[1:], values[1:])
	if err != nil {
		return "", nil, err
	}
	arguments := make([]any, 0, len(nextArguments)+2)
	arguments = append(arguments, value, value)
	arguments = append(arguments, nextArguments...)
	return fmt.Sprintf("(%s %s ? OR (%s = ? AND %s))", quoted, operator, quoted, nextQuery), arguments, nil
}

// validateSortKeys 校验排序键集合和分隔符。
func validateSortKeys(keys SortKeys, sep string) error {
	if len(keys) == 0 {
		return fmt.Errorf("%w: no sort keys", ErrInvalidCursor)
	}
	if sep == "" {
		return fmt.Errorf("%w: separator is empty", ErrInvalidCursor)
	}
	for _, key := range keys {
		if key == nil || key.field() == "" {
			return fmt.Errorf("%w: sort key is nil or empty", ErrInvalidCursor)
		}
	}
	return nil
}

// clauseColumn 将安全标识符转换为 GORM clause 列。
func clauseColumn(identifier string) (clause.Column, error) {
	parts := strings.Split(identifier, ".")
	switch len(parts) {
	case 1:
		if parts[0] != "" {
			return clause.Column{Name: parts[0]}, nil
		}
	case 2:
		if parts[0] != "" && parts[1] != "" {
			return clause.Column{Table: parts[0], Name: parts[1]}, nil
		}
	}
	return clause.Column{}, fmt.Errorf("invalid GORM identifier %q", identifier)
}
