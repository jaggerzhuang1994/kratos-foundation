package database

import (
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func newGORMConfig(conf *config_pb.Gorm, gormLogger gormlogger.Interface) *gorm.Config {
	result := &gorm.Config{}
	if conf.GetSkipDefaultTransaction() {
		result.SkipDefaultTransaction = conf.GetSkipDefaultTransaction()
	}
	if conf.GetDefaultTransactionTimeout() != nil {
		result.DefaultTransactionTimeout = conf.GetDefaultTransactionTimeout().AsDuration()
	}
	if conf.GetDefaultContextTimeout() != nil {
		result.DefaultContextTimeout = conf.GetDefaultContextTimeout().AsDuration()
	}
	if conf.GetFullSaveAssociations() {
		result.FullSaveAssociations = conf.GetFullSaveAssociations()
	}
	if conf.GetDisableAutomaticPing() {
		result.DisableAutomaticPing = conf.GetDisableAutomaticPing()
	}
	if conf.GetDisableForeignKeyConstraintWhenMigrating() {
		result.DisableForeignKeyConstraintWhenMigrating = conf.GetDisableForeignKeyConstraintWhenMigrating()
	}
	if conf.GetIgnoreRelationshipsWhenMigrating() {
		result.IgnoreRelationshipsWhenMigrating = conf.GetIgnoreRelationshipsWhenMigrating()
	}
	if conf.GetDisableNestedTransaction() {
		result.DisableNestedTransaction = conf.GetDisableNestedTransaction()
	}
	if conf.GetAllowGlobalUpdate() {
		result.AllowGlobalUpdate = conf.GetAllowGlobalUpdate()
	}
	if conf.GetQueryFields() {
		result.QueryFields = conf.GetQueryFields()
	}
	if conf.GetCreateBatchSize() != 0 {
		result.CreateBatchSize = int(conf.GetCreateBatchSize())
	}
	if conf.GetTranslateError() {
		result.TranslateError = conf.GetTranslateError()
	}
	if conf.GetPropagateUnscoped() {
		result.PropagateUnscoped = conf.GetPropagateUnscoped()
	}
	result.Logger = gormLogger
	return result
}

// MergeGORMConfig applies explicitly configured connection fields over global fields.
func mergeGORMConfig(base, override *config_pb.Gorm) *config_pb.Gorm {
	result := new(config_pb.Gorm)
	if base != nil {
		result = proto.Clone(base).(*config_pb.Gorm)
	}
	if override != nil {
		proto.Merge(result, override)
		// Duration 表示一个完整阈值，不能把覆盖值的秒/纳秒分别与全局值合并。
		// 显式 0s 也必须替换全局值，使当前连接可以关闭慢操作判断。
		if threshold := override.GetLogger().GetSlowThreshold(); threshold != nil {
			result.Logger.SlowThreshold = proto.Clone(threshold).(*durationpb.Duration)
		}
	}
	return result
}

type gormLogHandler struct {
	logger        log.Logger
	slowThreshold time.Duration
	fields        []any
	groups        []string
}

func newGORMLogger(base log.Logger, config *config_pb.GormLogger) gormlogger.Interface {
	level := gormlogger.Silent
	switch config.GetLevel() {
	case config_pb.GormLogger_INFO:
		level = gormlogger.Info
	case config_pb.GormLogger_WARN:
		level = gormlogger.Warn
	case config_pb.GormLogger_ERROR:
		level = gormlogger.Error
	}

	configValue := gormlogger.Config{
		SlowThreshold:             config.GetSlowThreshold().AsDuration(),
		IgnoreRecordNotFoundError: config.GetIgnoreRecordNotFoundError(),
		ParameterizedQueries:      config.GetParameterizedQueries(),
		LogLevel:                  level,
	}
	handler := &gormLogHandler{
		logger:        base,
		slowThreshold: configValue.SlowThreshold,
	}
	// 复用 GORM 对日志级别、慢查询和参数过滤的判定，只在输出边界转换为 Foundation 结构化字段。
	return gormlogger.NewSlogLogger(slog.New(handler), configValue)
}

func (handler *gormLogHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (handler *gormLogHandler) Handle(ctx context.Context, record slog.Record) error {
	logger := handler.logger.WithContext(ctx)
	if caller := gormLogCaller(record.PC); caller != "" {
		logger = logger.With(log.CallerKey, caller)
	}

	attributes := make([]slog.Attr, 0, record.NumAttrs())
	record.Attrs(func(attribute slog.Attr) bool {
		attributes = append(attributes, attribute)
		return true
	})

	fields := append([]any(nil), handler.fields...)
	fields = appendGORMLogAttributes(fields, handler.groups, attributes, record.Message == "SQL executed")
	if record.Message == "SQL executed" {
		// GORM slog logger 将查询字段放在 trace 组内；输出时展平，便于日志系统直接检索。
		fields = append([]any{"event", "gorm.query"}, fields...)
		if record.Level == slog.LevelWarn && handler.slowThreshold > 0 {
			fields = append(fields, "slow_threshold", handler.slowThreshold)
		}
		return logger.Log(gormLogLevel(record.Level), fields...)
	}

	message := formatGORMLogMessage(record.Message, attributes)
	logger = logger.With(fields...)
	switch gormLogLevel(record.Level) {
	case kratoslog.LevelDebug:
		logger.Debug(message)
	case kratoslog.LevelWarn:
		logger.Warn(message)
	case kratoslog.LevelError:
		logger.Error(message)
	default:
		logger.Info(message)
	}
	return nil
}

func (handler *gormLogHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	next := *handler
	next.fields = append([]any(nil), handler.fields...)
	next.fields = appendGORMLogAttributes(next.fields, handler.groups, attributes, false)
	return &next
}

func (handler *gormLogHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return handler
	}
	next := *handler
	next.groups = append(append([]string(nil), handler.groups...), name)
	return &next
}

func appendGORMLogAttributes(
	fields []any,
	groups []string,
	attributes []slog.Attr,
	isQuery bool,
) []any {
	for _, attribute := range attributes {
		attribute.Value = attribute.Value.Resolve()
		if attribute.Value.Kind() == slog.KindGroup {
			nextGroups := groups
			if attribute.Key != "" && !(isQuery && attribute.Key == "trace") {
				nextGroups = append(append([]string(nil), groups...), attribute.Key)
			}
			fields = appendGORMLogAttributes(fields, nextGroups, attribute.Value.Group(), isQuery)
			continue
		}
		if !isQuery && attribute.Key == "data" {
			continue
		}
		key := attribute.Key
		if isQuery && key == "error" {
			key = "err"
		}
		if len(groups) > 0 {
			key = strings.Join(append(append([]string(nil), groups...), key), ".")
		}
		fields = append(fields, key, attribute.Value.Any())
	}
	return fields
}

func formatGORMLogMessage(message string, attributes []slog.Attr) string {
	// GORM 的 callback 管理日志格式串自带换行，输出前压平以保持一条事件占一行。
	message = strings.ReplaceAll(message, "\n", " ")
	for _, attribute := range attributes {
		if attribute.Key != "data" {
			continue
		}
		data, ok := attribute.Value.Any().([]any)
		if ok {
			return fmt.Sprintf(message, data...)
		}
	}
	return message
}

func gormLogLevel(level slog.Level) kratoslog.Level {
	switch {
	case level >= slog.LevelError:
		return kratoslog.LevelError
	case level >= slog.LevelWarn:
		return kratoslog.LevelWarn
	case level >= slog.LevelInfo:
		return kratoslog.LevelInfo
	default:
		return kratoslog.LevelDebug
	}
}

func gormLogCaller(programCounter uintptr) string {
	if programCounter == 0 {
		return ""
	}
	frame, _ := runtime.CallersFrames([]uintptr{programCounter}).Next()
	if frame.File == "" || frame.Line <= 0 {
		return ""
	}
	return normalizeGORMLogCaller(frame.File + ":" + strconv.Itoa(frame.Line))
}

// normalizeGORMLogCaller 保留查询来源的末两级目录和行号，避免输出构建机绝对路径。
func normalizeGORMLogCaller(source string) string {
	source = strings.ReplaceAll(source, "\\", "/")
	colon := strings.LastIndexByte(source, ':')
	if colon <= 0 {
		return ""
	}
	line, err := strconv.Atoi(source[colon+1:])
	if err != nil || line <= 0 {
		return ""
	}
	if slash := strings.LastIndexByte(source[:colon], '/'); slash >= 0 {
		if parent := strings.LastIndexByte(source[:slash], '/'); parent >= 0 {
			source = source[parent+1:]
		}
	}
	return source
}
