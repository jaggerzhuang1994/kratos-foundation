package modules

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/jsonschema"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/jsonschema/draft_04"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/jsonschema/draft_06"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/jsonschema/draft_07"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/jsonschema/draft_201909"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/jsonschema/draft_202012"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/internal/proto"
	pgs "github.com/lyft/protoc-gen-star/v2"
	"google.golang.org/protobuf/encoding/protojson"
)

type Module struct {
	// ModuleBase 提供 protoc 插件构建上下文、诊断及产物登记。
	*pgs.ModuleBase
	// pluginOptions 保存本次生成请求解析后的插件参数。
	pluginOptions *proto.PluginOptions
	// mergeSchema 为可选外部 Schema，合并前要求与输出方言一致。
	mergeSchema jsonschema.Draft

	// optimizer 清理并优化入口消息所需的 Schema 集合。
	optimizer *OptimizerImpl
	// generator 将中间 Schema 转换为指定方言。
	generator *MultiDraftGenerator
	// serializer 根据输出后缀序列化 JSON 或 YAML。
	serializer *SerializerImpl
}

func NewModule() *Module {
	return &Module{ModuleBase: &pgs.ModuleBase{}}
}

func (m *Module) Name() string {
	return "JsonSchemaModule"
}

func (m *Module) InitContext(c pgs.BuildContext) {
	m.ModuleBase.InitContext(c)
	m.pluginOptions = proto.GetPluginOptions(c.Parameters())

	m.optimizer = NewOptimizerImpl()
	m.generator = NewMultiDraftGenerator(m.ModuleBase, m.pluginOptions)
	prettyJSON, err := c.Parameters().BoolDefault("pretty_json_output", true)
	m.CheckErr(err, "invalid pretty_json_output option")
	m.serializer = NewSerializerImpl(m.pluginOptions, prettyJSON)

	if m.pluginOptions.Merge != "" {
		m.loadMergeSchema()
	}

	m.Debugf("pluginOptions: %v", protojson.MarshalOptions{EmitUnpopulated: true}.Format(m.pluginOptions))
}

func (m *Module) loadMergeSchema() {
	var body []byte
	var err error
	if strings.HasPrefix(m.pluginOptions.Merge, "http://") || strings.HasPrefix(m.pluginOptions.Merge, "https://") {
		body, err = readRemoteMerge(m.pluginOptions.Merge)
	} else {
		body, err = os.ReadFile(m.pluginOptions.Merge)
	}
	m.CheckErr(err, fmt.Sprintf("failed read merge schema from %s", m.pluginOptions.Merge))
	var detectSchema struct {
		// Schema 用于从外部文档检测 JSON Schema 方言。
		Schema string `json:"$schema"`
	}
	err = m.serializer.Unserialize(body, &detectSchema, m.pluginOptions.Merge)
	m.CheckErr(err, "failed detect merge schema")

	switch detectSchema.Schema {
	case draft04Version:
		m.mergeSchema = &draft_04.Schema{}
	case draft06Version:
		m.mergeSchema = &draft_06.Schema{}
	case draft07Version:
		m.mergeSchema = &draft_07.Schema{}
	case draft201909Version:
		m.mergeSchema = &draft_201909.Schema{}
	case draft202012Version:
		m.mergeSchema = &draft_202012.Schema{}
	default:
		m.Failf("unsupported merge schema %s", detectSchema.Schema)
	}

	var formatExt = m.pluginOptions.Merge
	err = m.serializer.Unserialize(body, m.mergeSchema, formatExt)
	m.CheckErr(err, "failed serialize merge schema")
}

func (m *Module) Execute(targets map[string]pgs.File, packages map[string]pgs.Package) []pgs.Artifact {
	// Phase: Frontend IntermediateSchemaGenerate
	visitor := NewVisitor(m, m.pluginOptions)
	for _, pkg := range packages {
		m.CheckErr(pgs.Walk(visitor, pkg), fmt.Sprintf("failed to walk package %s", pkg.ProtoName().String()))
	}
	m.Debugf("# of IntermediateSchemas: %d", len(visitor.registry.GetKeys()))

	// Phase: Backend TargetSchemaGenerate
	m.Push("BackendPhase")
	visitor.registry.SortSchemas()

	for _, file := range targets {
		artifact := m.backendPhase(file, visitor.registry)
		if artifact != nil {
			m.AddArtifact(artifact)
		}
	}

	return m.Artifacts()
}

func (m *Module) backendPhase(file pgs.File, registry *jsonschema.Registry) pgs.Artifact {
	defer m.Push(file.Name().String()).Pop()
	m.Debugf("file options: %v", protojson.MarshalOptions{EmitUnpopulated: true}.Format(proto.GetFileOptions(file)))

	entrypointMessage := getEntrypointFromFile(file, m.pluginOptions)
	if entrypointMessage == nil {
		m.Logf("Cannot find matched entrypointMessage, Please check FileOptions")
		return nil
	}

	copiedRegistry := jsonschema.DeepCopyRegistry(registry)
	m.optimizer.Optimize(copiedRegistry, entrypointMessage)
	m.Debugf("# of Schemas After Optimized : %d", len(copiedRegistry.GetKeys()))

	fileOptions := proto.GetFileOptions(file)
	rootSchema := m.generator.Generate(copiedRegistry, entrypointMessage, fileOptions)
	if rootSchema == nil {
		m.Logf("Cannot generate rootSchema, Please check FileOptions or PluginOptions")
		return nil
	}

	if m.mergeSchema != nil {
		if m.mergeSchema.Schema() != rootSchema.Schema() {
			m.Fail("Root Schema does not match merge schema")
		}
		err := rootSchema.Merge(m.mergeSchema)
		m.CheckErr(err, "failed merge schema")
	}

	content, err := m.serializer.Serialize(rootSchema, file)
	m.CheckErr(err, fmt.Sprintf("Failed to serialize file %s", file.Name().String()))
	fileName := m.serializer.ToFileName(file)
	m.Debugf("generated file name: %s", fileName)

	return pgs.GeneratorFile{Name: fileName, Contents: string(content)}
}

func getEntrypointFromFile(file pgs.File, pluginOptions *proto.PluginOptions) pgs.Message {
	entryPointMessage := proto.GetEntrypointMessage(pluginOptions, proto.GetFileOptions(file))
	if entryPointMessage == "" {
		return nil
	}

	for _, message := range file.Messages() {
		if message.Name().String() == entryPointMessage {
			return message
		}
	}
	return nil
}

// readRemoteMerge 限制整个远程读取的时间与体积，避免代码生成被外部服务永久阻塞。
func readRemoteMerge(location string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Get(location)
	if err != nil {
		return nil, fmt.Errorf("download merge schema: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("download merge schema: HTTP status %d", response.StatusCode)
	}
	const maximumBytes = 8 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read merge schema: %w", err)
	}
	if len(body) > maximumBytes {
		return nil, fmt.Errorf("merge schema exceeds %d bytes", maximumBytes)
	}
	return body, nil
}
