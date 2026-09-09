package database

import (
	"bytes"
	"context"
	"database/sql/driver"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

func TestAESFieldCipherValidatesAndRoundTripsValues(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	cipher, err := newAESFieldCipher(&config_pb.DatabaseAes{Key: &key})
	if err != nil || cipher == nil {
		t.Fatalf("cipher=%v err=%v", cipher, err)
	}
	ctx := context.WithValue(context.Background(), aesFieldContextKey{}, aesFieldState{connection: "primary", cipher: cipher})
	field := &schema.Field{Name: "Secret"}
	plain := AESDecryptString("hello")
	encrypted, err := (&plain).Value(ctx, field, reflect.Value{}, plain)
	if err != nil || encrypted == "hello" {
		t.Fatalf("encrypted=%v err=%v", encrypted, err)
	}
	var restored AESDecryptString
	if err := (&restored).Scan(ctx, field, reflect.Value{}, encrypted); err != nil || restored != plain {
		t.Fatalf("restored=%q err=%v", restored, err)
	}
	bytes := AESDecryptBytes("bytes")
	encoded, err := (&bytes).Value(ctx, field, reflect.Value{}, bytes)
	var decoded AESDecryptBytes
	if err != nil || (&decoded).Scan(ctx, field, reflect.Value{}, encoded) != nil || string(decoded) != "bytes" {
		t.Fatalf("decoded=%q err=%v", decoded, err)
	}
}

func TestAESFieldCipherRejectsInvalidConfigAndMissingContext(t *testing.T) {
	if cipher, err := newAESFieldCipher(nil); err != nil || cipher != nil {
		t.Fatalf("empty cipher=%v err=%v", cipher, err)
	}
	bad := "not-base64"
	if _, err := newAESFieldCipher(&config_pb.DatabaseAes{Key: &bad}); err == nil {
		t.Fatal("invalid key accepted")
	}
	field := &schema.Field{Name: "Secret"}
	value := AESDecryptString("x")
	if _, err := (&value).Value(context.Background(), field, reflect.Value{}, value); !errors.Is(err, ErrAESConfigMissing) {
		t.Fatalf("error=%v", err)
	}
	var scanned AESDecryptString
	if err := (&scanned).Scan(context.Background(), field, reflect.Value{}, 123); err == nil {
		t.Fatal("invalid DB type accepted")
	}
}

func TestAESMapWritesEncryptEverySupportedDestination(t *testing.T) {
	for _, shape := range []string{"map", "map pointer", "map double pointer", "slice", "slice pointer", "slice double pointer"} {
		for _, withKey := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/key=%t", shape, withKey), func(t *testing.T) {
				connection := &config_pb.DBConnection{Driver: proto.String("sqlite3"), Dsn: filepath.Join(t.TempDir(), "aes.db")}
				if withKey {
					connection.Aes = &config_pb.DatabaseAes{Key: proto.String("MDEyMzQ1Njc4OWFiY2RlZg==")}
				}
				m := newSQLiteManager(t, &config_pb.Database{Default: proto.String("primary"), Connections: map[string]*config_pb.DBConnection{"primary": connection}})
				db := m.Connection(context.Background())
				if err := db.AutoMigrate(&aesMapValuesRecord{}); err != nil {
					t.Fatal(err)
				}
				values := map[string]any{"Secret": "plaintext secret", "Payload": []byte("plaintext bytes"), "Plain": "visible"}
				batch := []map[string]any{values}
				mapPointer := &values
				slicePointer := &batch
				var dest any
				switch shape {
				case "map":
					dest = values
				case "map pointer":
					dest = mapPointer
				case "map double pointer":
					dest = &mapPointer
				case "slice":
					dest = batch
				case "slice pointer":
					dest = slicePointer
				case "slice double pointer":
					dest = &slicePointer
				}
				err := db.Model(&aesMapValuesRecord{}).Create(dest).Error
				if !withKey {
					if !errors.Is(err, ErrAESConfigMissing) {
						t.Fatalf("Create() error = %v, want ErrAESConfigMissing", err)
					}
					var count int64
					if err := db.Model(&aesMapValuesRecord{}).Count(&count).Error; err != nil || count != 0 {
						t.Fatalf("count=%d err=%v", count, err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				var secret string
				var payload []byte
				p, _ := m.connectionFactory.pool("primary")
				if err := p.db.QueryRow("SELECT secret,payload FROM aes_map_values_records").Scan(&secret, &payload); err != nil {
					t.Fatal(err)
				}
				cipher := mustAESFieldCipher(t)
				plain, err := cipher.algorithm.DecryptString(secret, string(cipher.key))
				if err != nil || plain != "plaintext secret" {
					t.Errorf("stored secret=%q decrypted=%q err=%v", secret, plain, err)
				}
				plainBytes, err := cipher.algorithm.Decrypt(payload, cipher.key)
				if err != nil || string(plainBytes) != "plaintext bytes" {
					t.Errorf("stored payload=%q decrypted=%q err=%v", payload, plainBytes, err)
				}
				if values["Secret"] != "plaintext secret" || string(values["Payload"].([]byte)) != "plaintext bytes" {
					t.Fatalf("caller values changed: %#v", values)
				}
			})
		}
	}
}

func TestAESMapCreateSupportsGeneratedIDsAndReturning(t *testing.T) {
	for _, shape := range []string{"map", "slice", "slice pointer", "batches", "batches pointer"} {
		for _, returning := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/returning=%t", shape, returning), func(t *testing.T) {
				connection := &config_pb.DBConnection{Driver: proto.String("sqlite3"), Dsn: filepath.Join(t.TempDir(), "db.sqlite"), Aes: &config_pb.DatabaseAes{Key: proto.String("MDEyMzQ1Njc4OWFiY2RlZg==")}}
				mgr := newSQLiteManager(t, &config_pb.Database{Default: proto.String("primary"), Connections: map[string]*config_pb.DBConnection{"primary": connection}})
				db := mgr.Connection(context.Background())
				if err := db.AutoMigrate(&aesUpdateRecord{}); err != nil {
					t.Fatal(err)
				}
				records := []map[string]any{{"Secret": "a"}, {"Secret": "b"}, {"Secret": "c"}}
				model := db.Model(&aesUpdateRecord{})
				if returning {
					model = model.Clauses(clause.Returning{})
				}
				defer func() {
					if value := recover(); value != nil {
						t.Fatalf("Create panic: %v", value)
					}
				}()
				var result *gorm.DB
				wantRows := 3
				switch shape {
				case "map":
					result = model.Create(records[0])
					wantRows = 1
				case "slice":
					result = model.Create(records)
				case "slice pointer":
					result = model.Create(&records)
				case "batches":
					result = model.CreateInBatches(records, 2)
				case "batches pointer":
					result = model.CreateInBatches(&records, 2)
				}
				if result.Error != nil || result.RowsAffected != int64(wantRows) {
					t.Fatalf("Create result error=%v rows=%d want=%d", result.Error, result.RowsAffected, wantRows)
				}
				var loaded []aesUpdateRecord
				if err := db.Order("id").Find(&loaded).Error; err != nil {
					t.Fatal(err)
				}
				if len(loaded) != wantRows {
					t.Fatalf("loaded %d records, want %d", len(loaded), wantRows)
				}
				for i, record := range loaded {
					if record.ID == 0 || string(record.Secret) != []string{"a", "b", "c"}[i] {
						t.Fatalf("loaded record %d=%#v", i, record)
					}
				}
				if len(records) != 3 {
					t.Fatalf("caller slice changed size: %d", len(records))
				}
				for i, row := range records {
					if len(row) != 1 || row["Secret"] != []string{"a", "b", "c"}[i] {
						t.Fatalf("caller map %d changed: %#v", i, row)
					}
				}
			})
		}
	}
}

func TestAESMapCreateInBatchesPreservesCallerValues(t *testing.T) {
	for _, withKey := range []bool{false, true} {
		name := "no_key"
		if withKey {
			name = "with_key"
		}
		t.Run(name, func(t *testing.T) {
			connection := &config_pb.DBConnection{Driver: proto.String("sqlite3"), Dsn: filepath.Join(t.TempDir(), "db.sqlite")}
			if withKey {
				connection.Aes = &config_pb.DatabaseAes{Key: proto.String("MDEyMzQ1Njc4OWFiY2RlZg==")}
			}
			mgr := newSQLiteManager(t, &config_pb.Database{Default: proto.String("primary"), Connections: map[string]*config_pb.DBConnection{"primary": connection}})
			db := mgr.Connection(context.Background())
			if err := db.AutoMigrate(&aesMapValuesRecord{}); err != nil {
				t.Fatal(err)
			}
			records := []map[string]any{{"Secret": "a"}, {"Secret": "b"}, {"Secret": "c"}}
			result := db.Model(&aesMapValuesRecord{}).CreateInBatches(records, 2)
			var loaded []aesMapValuesRecord
			if err := db.Find(&loaded).Error; err != nil {
				t.Fatal(err)
			}
			if withKey {
				if result.Error != nil || result.RowsAffected != 3 || len(loaded) != 3 {
					t.Fatalf("result err=%v rows=%d loaded=%+v", result.Error, result.RowsAffected, loaded)
				}
				for i, want := range []string{"a", "b", "c"} {
					if string(loaded[i].Secret) != want || records[i]["Secret"] != want {
						t.Fatalf("row %d decrypted/caller data differs", i)
					}
				}
			} else if result.Error == nil || result.RowsAffected != 0 || len(loaded) != 0 {
				t.Fatalf("missing-key write result err=%v rows=%d loaded=%+v", result.Error, result.RowsAffected, loaded)
			}
		})
	}
}

func TestMapUpdatesPreserveModelPrimaryKey(t *testing.T) {
	for _, allowGlobal := range []bool{false, true} {
		name := "guarded"
		if allowGlobal {
			name = "global_allowed"
		}
		t.Run(name, func(t *testing.T) {
			mgr := newSQLiteManager(t, &config_pb.Database{Default: proto.String("primary"), Gorm: &config_pb.Gorm{AllowGlobalUpdate: proto.Bool(allowGlobal)}, Connections: map[string]*config_pb.DBConnection{"primary": {Driver: proto.String("sqlite3"), Dsn: filepath.Join(t.TempDir(), "db.sqlite")}}})
			db := mgr.Connection(context.Background())
			if err := db.AutoMigrate(&managerRecord{}); err != nil {
				t.Fatal(err)
			}
			records := []managerRecord{{ID: 1, Name: "first"}, {ID: 2, Name: "second"}}
			if err := db.Create(&records).Error; err != nil {
				t.Fatal(err)
			}
			result := db.Model(&records[0]).Updates(map[string]any{"Name": "changed"})
			var loaded []managerRecord
			if err := db.Order("id").Find(&loaded).Error; err != nil {
				t.Fatal(err)
			}
			if result.Error != nil || result.RowsAffected != 1 || records[0].Name != "changed" || len(loaded) != 2 || loaded[1].Name != "second" {
				t.Fatalf("map update lost model primary key: error=%v rows=%d records=%v", result.Error, result.RowsAffected, loaded)
			}
		})
	}
}

func TestMapUpdatesPreserveReturning(t *testing.T) {
	mgr := newSQLiteManager(t, &config_pb.Database{Default: proto.String("primary"), Connections: map[string]*config_pb.DBConnection{"primary": {Driver: proto.String("sqlite3"), Dsn: filepath.Join(t.TempDir(), "db.sqlite")}}})
	db := mgr.Connection(context.Background())
	if err := db.AutoMigrate(&managerRecord{}); err != nil {
		t.Fatal(err)
	}
	record := managerRecord{ID: 1, Name: "first"}
	if err := db.Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	defer func() {
		if v := recover(); v != nil {
			t.Fatalf("update RETURNING panic: %v", v)
		}
	}()
	if err := db.Model(&record).Where("id = ?", 1).Clauses(clause.Returning{}).Updates(map[string]any{"Name": "changed"}).Error; err != nil {
		t.Fatal(err)
	}
	if record.Name != "changed" {
		t.Fatalf("Returning record=%#v", record)
	}
}

type aesUpdateRecord struct {
	ID      int `gorm:"primaryKey"`
	Secret  AESDecryptString
	Payload AESDecryptBytes
	Name    string
}

func TestAESMapUpdatesKeepModelPlaintextWithAndWithoutReturning(t *testing.T) {
	for _, returning := range []bool{false, true} {
		for _, pointer := range []bool{false, true} {
			t.Run(fmt.Sprintf("returning=%t/pointer=%t", returning, pointer), func(t *testing.T) {
				mgr := newSQLiteManager(t, &config_pb.Database{Default: proto.String("primary"), Connections: map[string]*config_pb.DBConnection{"primary": {Driver: proto.String("sqlite3"), Dsn: filepath.Join(t.TempDir(), "db.sqlite"), Aes: &config_pb.DatabaseAes{Key: proto.String("MDEyMzQ1Njc4OWFiY2RlZg==")}}}})
				db := mgr.Connection(context.Background())
				if err := db.AutoMigrate(&aesUpdateRecord{}); err != nil {
					t.Fatal(err)
				}
				record := aesUpdateRecord{ID: 1, Secret: "original", Payload: AESDecryptBytes("original bytes"), Name: "first"}
				if err := db.Create(&record).Error; err != nil {
					t.Fatal(err)
				}
				values := map[string]any{"Secret": "changed", "Payload": []byte("changed bytes"), "Name": "changed"}
				var dest any = values
				if pointer {
					dest = &values
				}
				query := db.Model(&record).Where("id = ?", record.ID)
				if returning {
					query = query.Clauses(clause.Returning{})
				}
				defer func() {
					if value := recover(); value != nil {
						t.Fatalf("map update panic: %v", value)
					}
				}()
				if err := query.Updates(dest).Error; err != nil {
					t.Fatal(err)
				}
				if record.Secret != "changed" || string(record.Payload) != "changed bytes" || record.Name != "changed" {
					t.Fatalf("Model lost plaintext after update: %#v", record)
				}
				if values["Secret"] != "changed" || string(values["Payload"].([]byte)) != "changed bytes" {
					t.Fatalf("caller map changed: %#v", values)
				}
				var loaded aesUpdateRecord
				if err := db.First(&loaded, record.ID).Error; err != nil || loaded.Secret != "changed" || string(loaded.Payload) != "changed bytes" {
					t.Fatalf("loaded=%#v err=%v", loaded, err)
				}
				pool, _ := mgr.connectionFactory.pool("primary")
				var rawSecret string
				var rawPayload []byte
				if err := pool.db.QueryRow("SELECT secret,payload FROM aes_update_records WHERE id = 1").Scan(&rawSecret, &rawPayload); err != nil {
					t.Fatal(err)
				}
				if rawSecret == "changed" || string(rawPayload) == "changed bytes" {
					t.Fatalf("plaintext stored: secret=%q payload=%q", rawSecret, rawPayload)
				}
			})
		}
	}
}

type aesMapValuesRecord struct {
	Secret  AESDecryptString
	Payload AESDecryptBytes
	Plain   string
}

func TestAESPluginEncryptMapValuesCopiesAndSerializesOnlyAESFields(t *testing.T) {
	parsed, err := schema.Parse(
		&aesMapValuesRecord{},
		new(sync.Map),
		schema.NamingStrategy{},
	)
	if err != nil {
		t.Fatal(err)
	}
	cipher := mustAESFieldCipher(t)
	ctx := context.WithValue(context.Background(), aesFieldContextKey{}, aesFieldState{
		connection: "primary",
		cipher:     cipher,
	})
	original := map[string]any{
		"Secret":  "plain text",
		"Payload": []byte("plain bytes"),
		"Plain":   "visible",
		"Unknown": "untouched",
	}
	db := &gorm.DB{
		Config: &gorm.Config{},
		Statement: &gorm.Statement{
			Context: ctx,
			Schema:  parsed,
			Dest:    original,
		},
	}
	new(aesFieldPlugin).encryptMapValues(db)
	if db.Error != nil {
		t.Fatal(db.Error)
	}
	result, ok := db.Statement.Dest.(map[string]any)
	if !ok || reflect.ValueOf(result).Pointer() == reflect.ValueOf(original).Pointer() {
		t.Fatalf("encrypted destination = %#v", db.Statement.Dest)
	}
	textValuer, ok := result["Secret"].(driver.Valuer)
	if !ok {
		t.Fatalf("AES string value has no SQL serializer: %T", result["Secret"])
	}
	textValue, err := textValuer.Value()
	if err != nil {
		t.Fatal(err)
	}
	encryptedText, ok := textValue.(string)
	if !ok || encryptedText == "plain text" {
		t.Fatalf("encrypted string = %#v", result["Secret"])
	}
	restoredText, err := cipher.algorithm.DecryptString(encryptedText, string(cipher.key))
	if err != nil || restoredText != "plain text" {
		t.Fatalf("decrypted string = %q, %v", restoredText, err)
	}
	bytesValuer, ok := result["Payload"].(driver.Valuer)
	if !ok {
		t.Fatalf("AES bytes value has no SQL serializer: %T", result["Payload"])
	}
	bytesValue, err := bytesValuer.Value()
	if err != nil {
		t.Fatal(err)
	}
	encryptedBytes, ok := bytesValue.([]byte)
	if !ok || bytes.Equal(encryptedBytes, []byte("plain bytes")) {
		t.Fatalf("encrypted bytes = %#v", result["Payload"])
	}
	restoredBytes, err := cipher.algorithm.Decrypt(encryptedBytes, cipher.key)
	if err != nil || !bytes.Equal(restoredBytes, []byte("plain bytes")) {
		t.Fatalf("decrypted bytes = %q, %v", restoredBytes, err)
	}
	if result["Plain"] != "visible" || result["Unknown"] != "untouched" ||
		original["Secret"] != "plain text" || !bytes.Equal(original["Payload"].([]byte), []byte("plain bytes")) {
		t.Fatalf("map encryption changed unrelated or source values: result=%#v source=%#v", result, original)
	}
}

func TestAESPluginEncryptMapValuesRejectsMissingCipherAndWrongTypes(t *testing.T) {
	parsed, err := schema.Parse(
		&aesMapValuesRecord{},
		new(sync.Map),
		schema.NamingStrategy{},
	)
	if err != nil {
		t.Fatal(err)
	}
	plugin := new(aesFieldPlugin)

	nonMap := &gorm.DB{Config: &gorm.Config{}, Statement: &gorm.Statement{
		Context: context.Background(),
		Schema:  parsed,
		Dest:    aesMapValuesRecord{},
	}}
	plugin.encryptMapValues(nonMap)
	if nonMap.Error != nil {
		t.Fatalf("non-map destination error = %v", nonMap.Error)
	}

	withoutSchema := &gorm.DB{Config: &gorm.Config{}, Statement: &gorm.Statement{
		Context: context.Background(),
		Dest:    map[string]any{"Secret": "plain"},
	}}
	plugin.encryptMapValues(withoutSchema)
	if withoutSchema.Error != nil {
		t.Fatalf("schema-less destination error = %v", withoutSchema.Error)
	}

	missing := &gorm.DB{Config: &gorm.Config{}, Statement: &gorm.Statement{
		Context: context.Background(),
		Schema:  parsed,
		Dest: map[string]any{
			"Secret":  "plain",
			"Plain":   nil,
			"Unknown": "ignored",
		},
	}}
	plugin.encryptMapValues(missing)
	if !errors.Is(missing.Error, ErrAESConfigMissing) {
		t.Fatalf("missing cipher error = %v", missing.Error)
	}

	ctx := context.WithValue(context.Background(), aesFieldContextKey{}, aesFieldState{
		connection: "primary",
		cipher:     mustAESFieldCipher(t),
	})
	wrong := &gorm.DB{Config: &gorm.Config{}, Statement: &gorm.Statement{
		Context: ctx,
		Schema:  parsed,
		Dest: map[string]any{
			"Secret":  123,
			"Payload": "not bytes",
		},
	}}
	plugin.encryptMapValues(wrong)
	if wrong.Error == nil || !strings.Contains(wrong.Error.Error(), "unexpected value type") {
		t.Fatalf("wrong map value error = %v", wrong.Error)
	}
}
