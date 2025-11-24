package config

import (
	"github.com/go-kratos/kratos/v2/config"
	"github.com/go-kratos/kratos/v2/config/env"
)

const (
	JsonFormat  = "json"
	XmlFormat   = "xml"
	YamlFormat  = "yaml"
	ProtoFormat = "proto"
)

type textSource struct {
	key    string
	format string
	yaml   string
}

func NewTextFormatSource(key, format, yaml string) config.Source {
	return &textSource{key, format, yaml}
}

func (s *textSource) Load() ([]*config.KeyValue, error) {
	return []*config.KeyValue{
		{
			Key:    s.key,
			Format: s.format,
			Value:  []byte(s.yaml),
		},
	}, nil
}

func (s *textSource) Watch() (config.Watcher, error) {
	w, err := env.NewWatcher()
	if err != nil {
		return nil, err
	}
	return w, nil
}
