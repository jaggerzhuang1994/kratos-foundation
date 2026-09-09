package gormscope

import (
	"math"
	"strconv"
	"testing"
	"time"
)

func TestEverySortKeyDirectionImplementsFieldEncodingAndDecoding(t *testing.T) {
	t.Parallel()

	at := time.Unix(1700000000, 123).UTC()
	tests := []struct {
		name        string
		key         SortKey
		value       any
		wantEncoded string
		ascending   bool
		assertValue func(*testing.T, any)
	}{
		{
			name:        "time ascending",
			key:         TimeAscSortKey("created_at"),
			value:       at,
			wantEncoded: "1700000000000000123",
			ascending:   true,
			assertValue: func(t *testing.T, value any) {
				if got, ok := value.(time.Time); !ok || !got.Equal(at) {
					t.Fatalf("decoded time = %#v", value)
				}
			},
		},
		{
			name:        "time descending",
			key:         TimeDescSortKey("created_at"),
			value:       &at,
			wantEncoded: "1700000000000000123",
			ascending:   false,
			assertValue: func(t *testing.T, value any) {
				if got, ok := value.(time.Time); !ok || !got.Equal(at) {
					t.Fatalf("decoded time = %#v", value)
				}
			},
		},
		{
			name:        "number ascending",
			key:         NumberAscSortKey("score"),
			value:       int64(42),
			wantEncoded: "42",
			ascending:   true,
			assertValue: func(t *testing.T, value any) {
				if value != "42" {
					t.Fatalf("decoded number = %#v", value)
				}
			},
		},
		{
			name:        "number descending",
			key:         NumberDescSortKey("score"),
			value:       uint64(42),
			wantEncoded: "42",
			ascending:   false,
			assertValue: func(t *testing.T, value any) {
				if value != "42" {
					t.Fatalf("decoded number = %#v", value)
				}
			},
		},
		{
			name:        "string ascending",
			key:         StringAscSortKey("name"),
			value:       "alice",
			wantEncoded: "alice",
			ascending:   true,
			assertValue: func(t *testing.T, value any) {
				if value != "alice" {
					t.Fatalf("decoded string = %#v", value)
				}
			},
		},
		{
			name:        "string descending",
			key:         StringDescSortKey("name"),
			value:       "alice",
			wantEncoded: "alice",
			ascending:   false,
			assertValue: func(t *testing.T, value any) {
				if value != "alice" {
					t.Fatalf("decoded string = %#v", value)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := test.key.field(); got == "" {
				t.Fatal("field is empty")
			}
			if got := test.key.ascending(); got != test.ascending {
				t.Fatalf("ascending = %t, want %t", got, test.ascending)
			}
			encoded, err := test.key.encode(test.value)
			if err != nil || encoded != test.wantEncoded {
				t.Fatalf("encode() = (%q, %v), want %q", encoded, err, test.wantEncoded)
			}
			decoded, err := test.key.decode(encoded)
			if err != nil {
				t.Fatal(err)
			}
			test.assertValue(t, decoded)
		})
	}
}

type cursorNumericStringer struct {
	value string
}

func (value cursorNumericStringer) String() string {
	return value.value
}

func TestEncodeNumberSupportsEveryNumericKindAndNumericText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "int", value: int(-1), want: "-1"},
		{name: "int8", value: int8(-8), want: "-8"},
		{name: "int16", value: int16(-16), want: "-16"},
		{name: "int32", value: int32(-32), want: "-32"},
		{name: "int64 minimum", value: int64(math.MinInt64), want: "-9223372036854775808"},
		{name: "uint", value: uint(1), want: "1"},
		{name: "uint8", value: uint8(8), want: "8"},
		{name: "uint16", value: uint16(16), want: "16"},
		{name: "uint32", value: uint32(32), want: "32"},
		{name: "uint64 maximum", value: uint64(math.MaxUint64), want: "18446744073709551615"},
		{name: "uintptr", value: uintptr(64), want: "64"},
		{name: "float32", value: float32(1.25), want: "1.25"},
		{name: "float64", value: float64(-2.5), want: "-2.5"},
		{name: "numeric string", value: "001.50", want: "001.50"},
		{name: "numeric Stringer", value: cursorNumericStringer{value: "7/8"}, want: "7/8"},
	}
	key := sortKey{kind: numberKind}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := key.encode(test.value)
			if err != nil || got != test.want {
				t.Fatalf("encode(%T) = (%q, %v), want %q", test.value, got, err, test.want)
			}
		})
	}
}

func TestSortKeyEncodingRejectsNilWrongTypesAndUnknownKind(t *testing.T) {
	t.Parallel()

	var nilTime *time.Time
	tests := []struct {
		name  string
		key   sortKey
		value any
	}{
		{name: "nil time", key: sortKey{kind: timeKind}, value: nilTime},
		{name: "non-time", key: sortKey{kind: timeKind}, value: int64(1)},
		{name: "nil number", key: sortKey{kind: numberKind}, value: nil},
		{name: "non-numeric string", key: sortKey{kind: numberKind}, value: "NaN"},
		{name: "non-numeric Stringer", key: sortKey{kind: numberKind}, value: cursorNumericStringer{value: "not-a-number"}},
		{name: "non-number", key: sortKey{kind: numberKind}, value: true},
		{name: "non-string", key: sortKey{kind: stringKind}, value: []byte("name")},
		{name: "unknown kind", key: sortKey{kind: sortKind(255)}, value: "value"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if encoded, err := test.key.encode(test.value); err == nil {
				t.Fatalf("encode(%T) = %q, want error", test.value, encoded)
			}
		})
	}
}

func TestSortKeyDecodingCoversBoundariesAndErrors(t *testing.T) {
	t.Parallel()

	nanoseconds := int64(1700000000123456789)
	decoded, err := (sortKey{kind: timeKind}).decode(strconv.FormatInt(nanoseconds, 10))
	if err != nil {
		t.Fatal(err)
	}
	gotTime, ok := decoded.(time.Time)
	if !ok || gotTime.UnixNano() != nanoseconds || gotTime.Location() != time.UTC {
		t.Fatalf("decoded time = %#v, want UTC nanoseconds %d", decoded, nanoseconds)
	}
	decoded, err = (sortKey{kind: numberKind}).decode("-12.50")
	if err != nil || decoded != "-12.50" {
		t.Fatalf("decoded number = %#v, err = %v", decoded, err)
	}
	decoded, err = (sortKey{kind: stringKind}).decode("")
	if err != nil || decoded != "" {
		t.Fatalf("decoded empty string = %#v, err = %v", decoded, err)
	}

	for _, test := range []struct {
		name  string
		key   sortKey
		value string
	}{
		{name: "invalid time", key: sortKey{kind: timeKind}, value: "not-a-time"},
		{name: "invalid number", key: sortKey{kind: numberKind}, value: "NaN"},
		{name: "unknown kind", key: sortKey{kind: sortKind(255)}, value: "value"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if value, err := test.key.decode(test.value); err == nil {
				t.Fatalf("decode(%q) = %#v, want error", test.value, value)
			}
		})
	}
}
