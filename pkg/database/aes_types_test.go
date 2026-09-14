package database

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"gorm.io/gorm/schema"
)

func TestAESFieldValueTypesExposePlaintextAndGORMColumnTypes(t *testing.T) {
	text := AESDecryptString("plain text")
	if text.String() != "plain text" || text.GormDataType() != "string" ||
		text.GormDBDataType(nil, nil) != "text" {
		t.Fatalf("AES string type = value %q generic %q database %q", text.String(), text.GormDataType(), text.GormDBDataType(nil, nil))
	}
	binary := AESDecryptBytes{0, 1, 2, 255}
	if !bytes.Equal(binary.Bytes(), []byte{0, 1, 2, 255}) || binary.GormDataType() != "bytes" ||
		binary.GormDBDataType(nil, nil) != "blob" {
		t.Fatalf("AES bytes type = value %v generic %q database %q", binary.Bytes(), binary.GormDataType(), binary.GormDBDataType(nil, nil))
	}
	if !isAESFieldType(reflect.TypeFor[AESDecryptString]()) || !isAESFieldType(reflect.TypeFor[AESDecryptBytes]()) ||
		isAESFieldType(reflect.TypeFor[string]()) || isAESFieldType(nil) {
		t.Fatal("AES field type classification is incorrect")
	}
}

func TestAESFieldValueTypesResetNilAndRejectWrongInputs(t *testing.T) {
	field := &schema.Field{Name: "Secret"}
	text := AESDecryptString("previous")
	if err := (&text).Scan(context.Background(), field, reflect.Value{}, nil); err != nil || text != "" {
		t.Fatalf("string Scan(nil) = %q, %v", text, err)
	}
	binary := AESDecryptBytes("previous")
	if err := (&binary).Scan(context.Background(), field, reflect.Value{}, nil); err != nil || binary != nil {
		t.Fatalf("bytes Scan(nil) = %v, %v", binary, err)
	}

	missingCipher := context.WithValue(context.Background(), aesFieldContextKey{}, aesFieldState{connection: "archive"})
	if _, err := aesFieldCipherFromContext(missingCipher); !errors.Is(err, ErrAESConfigMissing) ||
		!strings.Contains(err.Error(), "archive") {
		t.Fatalf("missing connection cipher error = %v", err)
	}
	if _, err := (&text).Value(missingCipher, field, reflect.Value{}, text); !errors.Is(err, ErrAESConfigMissing) {
		t.Fatalf("string Value missing cipher error = %v", err)
	}
	if _, err := (&binary).Value(missingCipher, field, reflect.Value{}, binary); !errors.Is(err, ErrAESConfigMissing) {
		t.Fatalf("bytes Value missing cipher error = %v", err)
	}

	cipherContext := context.WithValue(context.Background(), aesFieldContextKey{}, aesFieldState{
		connection: "primary",
		cipher:     mustAESFieldCipher(t),
	})
	if _, err := (&text).Value(cipherContext, field, reflect.Value{}, "wrong type"); err == nil ||
		!strings.Contains(err.Error(), "unexpected value type") {
		t.Fatalf("string wrong value error = %v", err)
	}
	if _, err := (&binary).Value(cipherContext, field, reflect.Value{}, []byte("wrong type")); err == nil ||
		!strings.Contains(err.Error(), "unexpected value type") {
		t.Fatalf("bytes wrong value error = %v", err)
	}
	if err := (&binary).Scan(cipherContext, field, reflect.Value{}, 123); err == nil ||
		!strings.Contains(err.Error(), "unexpected database type") {
		t.Fatalf("bytes wrong database value error = %v", err)
	}
}

func TestAESFieldValueTypesSkipEmptyDecryption(t *testing.T) {
	field := &schema.Field{Name: "Secret"}
	contexts := []struct {
		name string
		ctx  context.Context
	}{
		{name: "without cipher", ctx: context.Background()},
		{name: "with cipher", ctx: context.WithValue(context.Background(), aesFieldContextKey{}, aesFieldState{
			connection: "primary", cipher: mustAESFieldCipher(t),
		})},
	}
	for _, state := range contexts {
		t.Run(state.name, func(t *testing.T) {
			for _, input := range []struct {
				name  string
				value any
			}{
				{name: "empty string", value: ""},
				{name: "empty bytes", value: []byte{}},
				{name: "nil bytes", value: []byte(nil)},
			} {
				t.Run(input.name, func(t *testing.T) {
					text := AESDecryptString("previous")
					if err := text.Scan(state.ctx, field, reflect.Value{}, input.value); err != nil || text != "" {
						t.Fatalf("string Scan = %q, %v", text, err)
					}
					binary := AESDecryptBytes("previous")
					if err := binary.Scan(state.ctx, field, reflect.Value{}, input.value); err != nil || len(binary) != 0 {
						t.Fatalf("bytes Scan = %v, %v", binary, err)
					}
				})
			}
		})
	}
}

func mustAESFieldCipher(t testing.TB) *aesFieldCipher {
	t.Helper()
	key := "MDEyMzQ1Njc4OWFiY2RlZg=="
	cipher, err := newAESFieldCipher(&config_pb.DatabaseAes{Key: &key})
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}
