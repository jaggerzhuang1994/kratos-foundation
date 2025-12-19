package modules

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jaggerzhuang1994/kratos-foundation/cmd/protoc-gen-jsonschema/pkg/proto"
	pgs "github.com/lyft/protoc-gen-star/v2"
	"sigs.k8s.io/yaml"
)

type BackendSerializer interface {
	Serialize(schema any, file pgs.File) ([]byte, error)
	Unserialize(data []byte, schema any, formatExt string) error
	ToFileName(file pgs.File) string
}

var _ BackendSerializer = (*SerializerImpl)(nil)

type SerializerImpl struct {
	pluginOptions *proto.PluginOptions
}

func NewSerializerImpl(pluginOptions *proto.PluginOptions) *SerializerImpl {
	return &SerializerImpl{pluginOptions: pluginOptions}
}

func (s *SerializerImpl) Serialize(schema any, file pgs.File) ([]byte, error) {
	outputFileSuffix := s.ToFileName(file)

	if strings.HasSuffix(outputFileSuffix, ".json") {
		return json.MarshalIndent(schema, "", "  ")
		//if s.pluginOptions.GetPrettyJsonOutput() {
		//	return json.MarshalIndent(schema, "", "  ")
		//} else {
		//	return json.Marshal(schema)
		//}
	} else if strings.HasSuffix(outputFileSuffix, ".yaml") || strings.HasSuffix(outputFileSuffix, ".yml") {
		return yaml.Marshal(schema)
	}

	return nil, fmt.Errorf("unsupported output file suffix: `%s`, suffix should be endsWith `.json`, `.yaml`, `.yml`", outputFileSuffix)
}

func (s *SerializerImpl) Unserialize(data []byte, schema any, formatExt string) error {
	if formatExt == "" {
		formatExt = s.pluginOptions.OutputFileSuffix
	}
	if strings.HasSuffix(formatExt, ".json") {
		return json.Unmarshal(data, schema)
	} else if strings.HasSuffix(formatExt, ".yaml") || strings.HasSuffix(formatExt, ".yml") {
		return yaml.Unmarshal(data, schema)
	}
	return fmt.Errorf("unsupported output_file_suffix: %s", formatExt)
}

func (s *SerializerImpl) ToFileName(file pgs.File) string {
	outputFileSuffix := s.pluginOptions.OutputFileSuffix
	return file.InputPath().SetExt(outputFileSuffix).String()
}
