package database

import (
	"context"
	"fmt"
	"reflect"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// AESDecryptString stores plaintext in Go and Base64 AES ciphertext in the database.
type AESDecryptString string

// String returns the plaintext value.
func (value AESDecryptString) String() string { return string(value) }

// GormDataType declares the generic GORM type.
func (AESDecryptString) GormDataType() string { return "string" }

// GormDBDataType declares the database column type.
func (AESDecryptString) GormDBDataType(*gorm.DB, *schema.Field) string { return "text" }

// Value encrypts a string with the selected connection's configuration.
func (*AESDecryptString) Value(
	ctx context.Context,
	field *schema.Field,
	dst reflect.Value,
	fieldValue any,
) (any, error) {
	fieldCipher, err := aesFieldCipherFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("encrypt database field %s: %w", field.Name, err)
	}
	plain, ok := fieldValue.(AESDecryptString)
	if !ok {
		return nil, fmt.Errorf("encrypt database field %s: unexpected value type %T", field.Name, fieldValue)
	}
	return fieldCipher.algorithm.EncryptString(string(plain), string(fieldCipher.key))
}

// Scan decrypts a string with the selected connection's configuration.
func (value *AESDecryptString) Scan(
	ctx context.Context,
	field *schema.Field,
	_ reflect.Value,
	dbValue any,
) error {
	if dbValue == nil {
		*value = ""
		return nil
	}
	var encrypted string
	switch dbValue := dbValue.(type) {
	case string:
		encrypted = dbValue
	case []byte:
		encrypted = string(dbValue)
	default:
		return fmt.Errorf("decrypt database field %s: unexpected database type %T", field.Name, dbValue)
	}
	// 空字段无需密钥或解密，同时清除接收对象可能残留的旧值。
	if len(encrypted) == 0 {
		*value = ""
		return nil
	}
	fieldCipher, err := aesFieldCipherFromContext(ctx)
	if err != nil {
		return fmt.Errorf("decrypt database field %s: %w", field.Name, err)
	}
	plain, err := fieldCipher.algorithm.DecryptString(encrypted, string(fieldCipher.key))
	if err != nil {
		return fmt.Errorf("decrypt database field %s: %w", field.Name, err)
	}
	*value = AESDecryptString(plain)
	return nil
}

// AESDecryptBytes stores plaintext in Go and AES ciphertext in the database.
type AESDecryptBytes []byte

// Bytes returns the plaintext bytes.
func (value AESDecryptBytes) Bytes() []byte { return []byte(value) }

// GormDataType declares the generic GORM type.
func (AESDecryptBytes) GormDataType() string { return "bytes" }

// GormDBDataType declares the database column type.
func (AESDecryptBytes) GormDBDataType(*gorm.DB, *schema.Field) string { return "blob" }

// Value encrypts bytes with the selected connection's configuration.
func (*AESDecryptBytes) Value(
	ctx context.Context,
	field *schema.Field,
	dst reflect.Value,
	fieldValue any,
) (any, error) {
	fieldCipher, err := aesFieldCipherFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("encrypt database field %s: %w", field.Name, err)
	}
	plain, ok := fieldValue.(AESDecryptBytes)
	if !ok {
		return nil, fmt.Errorf("encrypt database field %s: unexpected value type %T", field.Name, fieldValue)
	}
	return fieldCipher.algorithm.Encrypt([]byte(plain), fieldCipher.key)
}

// Scan decrypts bytes with the selected connection's configuration.
func (value *AESDecryptBytes) Scan(
	ctx context.Context,
	field *schema.Field,
	_ reflect.Value,
	dbValue any,
) error {
	if dbValue == nil {
		*value = nil
		return nil
	}
	var encrypted []byte
	switch dbValue := dbValue.(type) {
	case []byte:
		encrypted = dbValue
	case string:
		encrypted = []byte(dbValue)
	default:
		return fmt.Errorf("decrypt database field %s: unexpected database type %T", field.Name, dbValue)
	}
	// 空字段无需密钥或解密，同时清除接收对象可能残留的旧值。
	if len(encrypted) == 0 {
		*value = nil
		return nil
	}
	fieldCipher, err := aesFieldCipherFromContext(ctx)
	if err != nil {
		return fmt.Errorf("decrypt database field %s: %w", field.Name, err)
	}
	plain, err := fieldCipher.algorithm.Decrypt(encrypted, fieldCipher.key)
	if err != nil {
		return fmt.Errorf("decrypt database field %s: %w", field.Name, err)
	}
	*value = append((*value)[:0], plain...)
	return nil
}
