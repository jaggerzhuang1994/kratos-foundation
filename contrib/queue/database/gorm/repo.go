package gorm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	databasequeue "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/queue/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/queue"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ConnectionProvider 每次按 Context 解析连接；可直接使用 pkg/database.Manager。
// Insert 可返回调用方事务；消费方法必须返回不含外层事务的会话，不得附加过滤 Scope。
type ConnectionProvider interface {
	Connection(context.Context) *gorm.DB
}

// Factory 创建独立、非 nil 的业务模型，填充自定义字段；队列字段随后由 Repo 覆盖。
// 输入是独立快照；工厂不得启动事务、保留模型引用或执行外部副作用。
type Factory[T Entity] func(context.Context, *databasequeue.TaskRecord) (T, error)

// Config 控制单个仓储的完成记录保留策略，在构造时复制，不支持热更新。
type Config struct {
	// RetainCompleted 为 true 时保留成功任务及完成时间；false 时成功即删除。
	RetainCompleted bool `json:"retain_completed"`
}

// Repo 为一个业务模型和表提供队列仓储。借用连接，表迁移与 cleanup 由业务负责。
type Repo[T Entity] struct {
	// provider 按上下文借用数据库连接；消费操作不允许使用外层事务。
	provider ConnectionProvider
	// factory 创建独立业务模型并填充自定义字段。
	factory Factory[T]
	// table 保存实际访问的物理表名。
	table string
	// modelTable 保存模型声明的表名，用于校验工厂返回模型。
	modelTable string
	// entityType 保存模型指针指向的类型，用于分配独立查询实例。
	entityType reflect.Type
	// retainCompleted 固定成功记录保留策略；构造后不热更新。
	retainCompleted bool
	// timestampPrecision 是当前方言 timestamp 列的默认精度，用于避免边界提前。
	timestampPrecision time.Duration
	// timestampMin 和 timestampMax 限制 MySQL TIMESTAMP 的可写 UTC 范围；SQLite 不限制。
	timestampMin time.Time
	timestampMax time.Time
}

// NewRepo 校验模型和方言，不查询或迁移表，不启动后台任务。
// T 必须为按值匿名嵌入 Model 的结构体指针；目前支持 SQLite 和 MySQL。
func NewRepo[T Entity](ctx context.Context, provider ConnectionProvider, factory Factory[T], config Config) (*Repo[T], error) {
	return newRepo(ctx, provider, factory, "", config)
}

// table 仅供框架简单模式选择物理表；扩展模式始终使用业务模型的固定 TableName。
func newRepo[T Entity](ctx context.Context, provider ConnectionProvider, factory Factory[T], table string, config Config) (*Repo[T], error) {
	typ := reflect.TypeFor[T]()
	if typ.Kind() != reflect.Pointer || typ.Elem().Kind() != reflect.Struct || factory == nil {
		return nil, errors.New("queue model must be a struct pointer with a factory")
	}
	field, ok := typ.Elem().FieldByName("Model")
	if !ok || !field.Anonymous || field.Type != reflect.TypeFor[Model]() {
		return nil, errors.New("queue model must embed Model by value")
	}
	entity := reflect.New(typ.Elem()).Interface().(T)
	if entity.QueueModel() != reflect.ValueOf(entity).Elem().FieldByName("Model").Addr().Interface().(*Model) {
		return nil, errors.New("QueueModel must return the embedded Model")
	}
	modelTable := entity.TableName()
	if table == "" {
		table = modelTable
	}
	if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(table) {
		return nil, errors.New("queue table must be a fixed simple identifier")
	}
	db := provider.Connection(ctx)
	if db.Error != nil {
		return nil, fmt.Errorf("resolve queue database: %w", db.Error)
	}
	var timestampPrecision time.Duration
	var timestampMin time.Time
	var timestampMax time.Time
	switch db.Dialector.Name() {
	case "sqlite":
		timestampPrecision = time.Millisecond
	case "mysql":
		timestampPrecision = time.Second
		timestampMin = time.Date(1970, time.January, 1, 0, 0, 1, 0, time.UTC)
		timestampMax = time.Date(2038, time.January, 19, 3, 14, 7, 0, time.UTC)
	default:
		return nil, errors.New("unsupported queue database dialect")
	}
	statement := &gorm.Statement{DB: db}
	if err := statement.Parse(entity); err != nil {
		return nil, fmt.Errorf("parse queue model: %w", err)
	}
	// 固定基础列的映射和唯一主键，防止自定义字段遮蔽租约或改变任务身份。
	for _, name := range []string{"Status", "CompletedAt", "ID", "Generation", "Data", "Attempts", "Token", "AvailableAt", "ReservedUntil", "Failed", "FailureReason", "FailedAt"} {
		f := statement.Schema.LookUpField(name)
		base, _ := reflect.TypeFor[Model]().FieldByName(name)
		if f == nil || len(f.BindNames) != 2 || f.BindNames[0] != "Model" || f.StructField.Tag != base.Tag || f.DBName != strings.Split(strings.TrimPrefix(base.Tag.Get("gorm"), "column:"), ";")[0] || !f.Creatable || !f.Updatable || !f.Readable {
			return nil, fmt.Errorf("queue model overrides managed field %s", name)
		}
		for _, other := range statement.Schema.Fields {
			if other != f && other.DBName == f.DBName {
				return nil, errors.New("queue model duplicates a managed column")
			}
		}
	}
	if len(statement.Schema.PrimaryFields) != 1 || statement.Schema.PrimaryFields[0].Name != "ID" || len(statement.Schema.QueryClauses) != 0 || len(statement.Schema.DeleteClauses) != 0 {
		return nil, errors.New("queue model cannot change primary key or use soft delete")
	}
	return &Repo[T]{
		provider: provider, factory: factory, table: table, modelTable: modelTable,
		entityType: typ.Elem(), retainCompleted: config.RetainCompleted,
		timestampPrecision: timestampPrecision, timestampMin: timestampMin, timestampMax: timestampMax,
	}, nil
}

func (r *Repo[T]) entity() T { return reflect.New(r.entityType).Interface().(T) }

func (r *Repo[T]) db(ctx context.Context) *gorm.DB {
	// Session 复制 Config，转换重复键错误且不修改业务连接；跳过业务钩子，防止改写租约。
	db := r.provider.Connection(ctx).Session(&gorm.Session{NewDB: true, SkipHooks: true})
	db.Config.TranslateError = true
	return db.WithContext(ctx).Model(r.entity()).Table(r.table)
}

// 消费状态必须在返回前提交，拒绝已打开的外层事务，避免把未提交租约交给 Handler。
func (r *Repo[T]) consumerDB(ctx context.Context) *gorm.DB {
	db := r.db(ctx)
	if _, ok := db.Statement.ConnPool.(gorm.TxCommitter); ok {
		db.Error = errors.Join(db.Error, errors.New("queue consumption cannot use an outer transaction"))
	}
	return db
}

func (r *Repo[T]) validateTimestamp(name string, at time.Time) error {
	if r.timestampMin.IsZero() {
		return nil
	}
	value := timestampCeil(at, r.timestampPrecision)
	if at.IsZero() || value.Before(r.timestampMin) || value.After(r.timestampMax) {
		return fmt.Errorf("queue %s is outside the MySQL timestamp range", name)
	}
	return nil
}

// Insert 将任务和工厂自定义字段写入同一行，可复用业务 Context 中的事务。
func (r *Repo[T]) Insert(ctx context.Context, record *databasequeue.TaskRecord) error {
	if record == nil || record.Task.ID == "" || len(record.Task.ID) > 128 || strings.TrimSpace(record.Task.MessageVersion) == "" || strings.TrimSpace(record.Task.ID) == "" || record.Attempts < 0 || len(record.Token) > 36 || len(record.FailureReason) > 128 {
		return errors.New("invalid queue task")
	}
	if err := r.validateTimestamp("available_at", record.Task.AvailableAt); err != nil {
		return err
	}
	if record.Failed {
		if err := r.validateTimestamp("failed_at", record.FailedAt); err != nil {
			return err
		}
	}
	snapshot := *record
	snapshot.Task = *record.Task.Clone()
	// 在调用工厂前编码，业务工厂不能修改框架即将保存的任务内容。
	data, err := json.Marshal(taskData{
		MessageVersion: snapshot.Task.MessageVersion,
		Payload:        snapshot.Task.Payload,
		Headers:        snapshot.Task.Headers,
		CreatedAt:      snapshot.Task.CreatedAt,
	})
	if err != nil {
		return fmt.Errorf("encode queue task: %w", err)
	}
	entity, err := r.factory(ctx, &snapshot)
	if err != nil {
		return fmt.Errorf("create queue model: %w", err)
	}
	if reflect.ValueOf(entity).IsNil() || entity.QueueModel() == nil || entity.TableName() != r.modelTable {
		return errors.New("factory returned an invalid queue model")
	}
	model := Model{Status: StatusPending, ID: taskID(record.Task.ID), Generation: uuid.NewString(), Data: data, AvailableAt: timestampCeil(record.Task.AvailableAt, r.timestampPrecision), Attempts: record.Attempts, Token: record.Token, Failed: record.Failed, FailureReason: record.FailureReason}
	if !record.ReservedUntil.IsZero() {
		model.ReservedUntil = deadlineMillis(record.ReservedUntil)
		model.Status = StatusRunning
	}
	if record.Failed {
		model.FailedAt = timestampPointer(record.FailedAt, r.timestampPrecision)
		model.Status = StatusFailed
	}
	*entity.QueueModel() = model
	result := r.db(ctx).Omit(clause.Associations).Create(entity)
	if errors.Is(result.Error, gorm.ErrDuplicatedKey) {
		return queue.ErrDuplicate
	}
	if result.Error != nil {
		return fmt.Errorf("insert queue task: %w", result.Error)
	}
	return nil
}
