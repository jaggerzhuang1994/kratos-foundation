// Package gormscope provides reusable, dialect-aware scopes for GORM. It does not
// re-export GORM's API; applications should continue importing gorm.io/gorm.
package gormscope

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Scope is a GORM query scope.
type Scope func(*gorm.DB) *gorm.DB

// EscapeLike 使用 ! 转义 SQL LIKE 通配符，应与本包的 LIKE scope 配合使用。
func EscapeLike(value string) string {
	value = strings.ReplaceAll(value, `!`, `!!`)
	value = strings.ReplaceAll(value, `%`, `!%`)
	return strings.ReplaceAll(value, `_`, `!_`)
}

// Like 使用显式模式过滤列，调用方应先用 EscapeLike 处理其中的字面值。
func Like(column, pattern string) Scope {
	return func(db *gorm.DB) *gorm.DB {
		quoted, err := QuoteIdentifier(db, column)
		if err != nil {
			return addError(db, err)
		}
		return db.Where(quoted+` LIKE ? ESCAPE '!'`, pattern)
	}
}

// Contains 匹配包含指定字面值的列。
func Contains(column, literal string) Scope {
	return Like(column, "%"+EscapeLike(literal)+"%")
}

// Prefix 匹配以指定字面值开头的列。
func Prefix(column, literal string) Scope {
	return Like(column, EscapeLike(literal)+"%")
}

// Suffix 匹配以指定字面值结尾的列。
func Suffix(column, literal string) Scope {
	return Like(column, "%"+EscapeLike(literal))
}

// TimeBetween 对非空时间边界应用包含端点的过滤条件。
func TimeBetween(column string, begin, end *time.Time) Scope {
	return func(db *gorm.DB) *gorm.DB {
		if begin == nil && end == nil {
			return db
		}
		quoted, err := QuoteIdentifier(db, column)
		if err != nil {
			return addError(db, err)
		}
		if begin != nil {
			db = db.Where(quoted+" >= ?", *begin)
		}
		if end != nil {
			db = db.Where(quoted+" <= ?", *end)
		}
		return db
	}
}

// Paginate 应用从 1 开始的页码和带上限的分页大小。
func Paginate(page, pageSize, maximumPageSize int) Scope {
	effectivePageSize := pageSize
	if maximumPageSize > 0 && effectivePageSize > maximumPageSize {
		effectivePageSize = maximumPageSize
	}
	return func(db *gorm.DB) *gorm.DB {
		if page < 1 {
			return addError(db, errors.New("GORM page must be at least 1"))
		}
		if pageSize < 1 {
			return addError(db, errors.New("GORM page size must be positive"))
		}
		// 先验证乘法范围，避免溢出为负数后被 GORM 当作取消 offset。
		if page-1 > math.MaxInt/effectivePageSize {
			return addError(db, errors.New("GORM page offset exceeds integer range"))
		}
		return db.Offset((page - 1) * effectivePageSize).Limit(effectivePageSize)
	}
}

// QuoteIdentifier 使用当前方言安全引用 column 或 table.column 标识符。
func QuoteIdentifier(db *gorm.DB, identifier string) (string, error) {
	if db == nil || db.Config == nil || db.Config.Dialector == nil {
		return "", errors.New("GORM database or dialect is nil")
	}
	parts := strings.Split(identifier, ".")
	if len(parts) > 2 {
		return "", fmt.Errorf("invalid GORM identifier %q", identifier)
	}
	var result bytes.Buffer
	for index, part := range parts {
		if part == "" || strings.ContainsRune(part, 0) {
			return "", fmt.Errorf("invalid GORM identifier %q", identifier)
		}
		if index > 0 {
			result.WriteByte('.')
		}
		db.Config.Dialector.QuoteTo(&result, part)
	}
	return result.String(), nil
}

// addError 在非空 GORM 句柄上记录错误并返回原句柄。
func addError(db *gorm.DB, err error) *gorm.DB {
	if db != nil {
		_ = db.AddError(err)
	}
	return db
}
