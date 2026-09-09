package database

import (
	"context"
	stdaes "crypto/aes"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"strings"

	foundationaes "github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/crypto/aes"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"gorm.io/gorm"
)

// ErrAESConfigMissing 表示当前连接未配置 AES 字段密钥。
var ErrAESConfigMissing = errors.New("database AES field encryption is not configured")

type aesFieldContextKey struct{}

type aesFieldCipher struct {
	algorithm foundationaes.Cipher
	key       []byte
}

type aesFieldState struct {
	connection string
	cipher     *aesFieldCipher
}

// aesFieldPlugin 向 GORM serializer 注入固定连接的加密状态。
type aesFieldPlugin struct {
	state aesFieldState
}

// newAESFieldPlugin 在每个独立 GORM 根实例中固定连接的加密配置。
func newAESFieldPlugin(connection string, config *config_pb.DatabaseAes) (*aesFieldPlugin, error) {
	cipher, err := newAESFieldCipher(config)
	if err != nil {
		return nil, fmt.Errorf("configure database connection %q AES fields: %w", connection, err)
	}
	return &aesFieldPlugin{state: aesFieldState{connection: connection, cipher: cipher}}, nil
}

func newAESFieldCipher(config *config_pb.DatabaseAes) (*aesFieldCipher, error) {
	if config == nil || strings.TrimSpace(config.GetKey()) == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(config.GetKey()))
	if err != nil {
		return nil, fmt.Errorf("decode base64 key: %w", err)
	}
	if _, err = stdaes.NewCipher(key); err != nil {
		return nil, fmt.Errorf("validate key: %w", err)
	}

	var algorithm foundationaes.Cipher
	switch config.GetAlgorithm() {
	case config_pb.DatabaseAes_UNKNOWN, config_pb.DatabaseAes_CBC:
		algorithm = foundationaes.CBC{}
	default:
		return nil, fmt.Errorf("unsupported algorithm %q", config.GetAlgorithm())
	}
	return &aesFieldCipher{algorithm: algorithm, key: key}, nil
}

// Name 返回 GORM 插件名。
func (*aesFieldPlugin) Name() string {
	return "kratos_foundation:aes_fields"
}

// Initialize 注册当前连接的 AES 状态与 map 加密回调。
func (plugin *aesFieldPlugin) Initialize(db *gorm.DB) error {
	callbackName := plugin.Name() + ":context"
	if err := db.Callback().Create().Before("gorm:create").
		Register(callbackName, plugin.injectCreate); err != nil {
		return fmt.Errorf("register gorm:create callback: %w", err)
	}
	if err := db.Callback().Update().Before("gorm:update").
		Register(callbackName, plugin.injectWrite); err != nil {
		return fmt.Errorf("register gorm:update callback: %w", err)
	}
	if err := db.Callback().Query().Before("gorm:query").
		Register(callbackName, plugin.inject); err != nil {
		return fmt.Errorf("register gorm:query callback: %w", err)
	}
	if err := db.Callback().Row().Before("gorm:row").
		Register(callbackName, plugin.inject); err != nil {
		return fmt.Errorf("register gorm:row callback: %w", err)
	}
	if err := db.Callback().Raw().Before("gorm:raw").
		Register(callbackName, plugin.inject); err != nil {
		return fmt.Errorf("register gorm:raw callback: %w", err)
	}
	return nil
}

func (plugin *aesFieldPlugin) inject(db *gorm.DB) {
	if db.Statement == nil {
		return
	}
	db.Statement.Context = context.WithValue(db.Statement.Context, aesFieldContextKey{}, plugin.state)
}

func (plugin *aesFieldPlugin) injectCreate(db *gorm.DB) {
	plugin.injectWrite(db)
	// Create 的结果接收目标随 map 副本一起切换；Update 的 ReflectValue 必须
	// 保留 GORM 选定的 Model，主键 WHERE、字段回写与 RETURNING 都依赖它。
	switch db.Statement.Dest.(type) {
	case map[string]any:
		db.Statement.ReflectValue = reflect.ValueOf(db.Statement.Dest)
	case *[]map[string]any:
		db.Statement.ReflectValue = reflect.ValueOf(db.Statement.Dest).Elem()
	}
}

func (plugin *aesFieldPlugin) injectWrite(db *gorm.DB) {
	plugin.inject(db)
	plugin.encryptMapValues(db)
}

func (*aesFieldPlugin) encryptMapValues(db *gorm.DB) {
	if db.Statement.Schema == nil {
		return
	}
	// GORM 的 map 写入绕过字段 serializer；解除指针包装后复制每条记录，
	// 仅替换本次语句的数据，不能把密文写回调用方持有的 map 或切片。
	values := reflect.ValueOf(db.Statement.Dest)
	for values.IsValid() && values.Kind() == reflect.Pointer {
		if values.IsNil() {
			return
		}
		values = values.Elem()
	}
	if !values.IsValid() {
		return
	}
	switch values := values.Interface().(type) {
	case map[string]any:
		db.Statement.Dest = encryptAESMap(db, values)
	case []map[string]any:
		result := make([]map[string]any, len(values))
		for index, row := range values {
			result[index] = encryptAESMap(db, row)
		}
		// GORM 的 INSERT 支持两种切片形态，但 RETURNING 的 map 扫描只支持
		// 切片指针；保持副本可扫描，避免自增主键返回时落入结构体扫描分支。
		db.Statement.Dest = &result
	default:
		return
	}
}

func encryptAESMap(db *gorm.DB, values map[string]any) map[string]any {
	result := make(map[string]any, len(values))
	model := reflect.New(db.Statement.Schema.ModelType).Elem()
	for name, value := range values {
		result[name] = value
		field := db.Statement.Schema.LookUpField(name)
		if field == nil || !isAESFieldType(field.FieldType) {
			continue
		}
		if _, err := aesFieldCipherFromContext(db.Statement.Context); err != nil {
			db.AddError(fmt.Errorf("encrypt database field %s: %w", field.Name, err))
			continue
		}
		if value == nil {
			continue
		}
		switch field.FieldType {
		case reflect.TypeFor[AESDecryptString]():
			if plain, ok := value.(string); ok {
				value = AESDecryptString(plain)
			}
			if _, ok := value.(AESDecryptString); !ok {
				db.AddError(fmt.Errorf("encrypt database field %s: unexpected value type %T", field.Name, value))
				continue
			}
		case reflect.TypeFor[AESDecryptBytes]():
			if plain, ok := value.([]byte); ok {
				value = AESDecryptBytes(plain)
			}
			if _, ok := value.(AESDecryptBytes); !ok {
				db.AddError(fmt.Errorf("encrypt database field %s: unexpected value type %T", field.Name, value))
				continue
			}
		}
		if err := field.Set(db.Statement.Context, model, value); err != nil {
			db.AddError(fmt.Errorf("set database field %s for encryption: %w", field.Name, err))
			continue
		}
		// 使用字段原生 serializer：SQL 绑定时加密，GORM Updates 回写 Model 时
		// 则取其中的明文，避免把密文写进业务对象或在下一次 Save 时重复加密。
		result[name], _ = field.ValueOf(db.Statement.Context, model)
	}
	return result
}

func isAESFieldType(fieldType reflect.Type) bool {
	return fieldType == reflect.TypeFor[AESDecryptString]() ||
		fieldType == reflect.TypeFor[AESDecryptBytes]()
}

func aesFieldCipherFromContext(ctx context.Context) (*aesFieldCipher, error) {
	state, ok := ctx.Value(aesFieldContextKey{}).(aesFieldState)
	if !ok {
		return nil, ErrAESConfigMissing
	}
	if state.cipher == nil {
		return nil, fmt.Errorf("%w for connection %q", ErrAESConfigMissing, state.connection)
	}
	return state.cipher, nil
}
