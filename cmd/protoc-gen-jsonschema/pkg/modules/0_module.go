package modules

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/pkg/jsonschema"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/pkg/jsonschema/draft_04"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/pkg/jsonschema/draft_06"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/pkg/jsonschema/draft_07"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/pkg/jsonschema/draft_201909"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/pkg/jsonschema/draft_202012"
	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/pkg/proto"
	pgs "github.com/lyft/protoc-gen-star/v2"
	"google.golang.org/protobuf/encoding/protojson"
)

type Module struct {
	*pgs.ModuleBase
	pluginOptions *proto.PluginOptions
	mergeSchema   jsonschema.Draft

	optimizer  BackendOptimizer
	generator  BackendTargetGenerator
	serializer BackendSerializer
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

	m.optimizer = NewOptimizerImpl(m.ModuleBase, m.pluginOptions)
	m.generator = NewMultiDraftGenerator(m.ModuleBase, m.pluginOptions)
	m.serializer = NewSerializerImpl(m.pluginOptions)

	if m.pluginOptions.Merge != "" {
		m.loadMergeSchema()
	}

	m.Debugf("pluginOptions: %v", protojson.MarshalOptions{EmitUnpopulated: true}.Format(m.pluginOptions))
}

func (m *Module) loadMergeSchema() {
	var body []byte
	var err error
	if strings.HasPrefix(m.pluginOptions.Merge, "http://") || strings.HasPrefix(m.pluginOptions.Merge, "https://") {
		var resp *http.Response
		resp, err = http.Get(m.pluginOptions.Merge)
		if err == nil {
			defer resp.Body.Close()
			body, err = io.ReadAll(resp.Body)
		}
	} else {
		body, err = os.ReadFile(m.pluginOptions.Merge)
	}
	m.CheckErr(err, fmt.Sprintf("failed read merge schema from %s", m.pluginOptions.Merge))
	var detectSchema struct {
		Schema string `json:"$schema"`
	}
	err = json.Unmarshal(body, &detectSchema)
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
	m.Debugf("FileOptions: %v", protojson.MarshalOptions{EmitUnpopulated: true}.Format(proto.GetFileOptions(file)))

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
		var err error
		err = rootSchema.Merge(m.mergeSchema)
		m.CheckErr(err, "failed merge schema")
	}

	content, err := m.serializer.Serialize(rootSchema, file)
	m.CheckErr(err, fmt.Sprintf("Failed to serialize file %s", file.Name().String()))
	fileName := m.serializer.ToFileName(file)
	m.Debugf("GeneratedFileName: %s", fileName)

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
