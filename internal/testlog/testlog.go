// Package testlog 为跨领域测试配置进程级 Logger；同一包中的调用须串行执行。
package testlog

import (
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
)

// New 通过环境变量配置下一次日志初始化，不依赖已暂停的公开 Update 入口。
// 调用方须先释放此前 Logger；环境变量保留到 cleanup，以支持所有者退出后的全局日志。
func New(config Config) (logger log.Logger, cleanup func(), err error) {
	values := map[string]string{
		log.EnvLevel: config.Level.String(), log.EnvFilterEmpty: strconv.FormatBool(config.FilterEmpty),
		log.EnvFilterKeys: strings.Join(config.FilterKeys, ","), log.EnvTimeFormat: config.TimeFormat,
		log.EnvStdDisable: strconv.FormatBool(config.Std.Disable), log.EnvStdLevel: config.Std.Level.String(),
		log.EnvStdFilterKeys: strings.Join(config.Std.FilterKeys, ","),
		log.EnvFileDisable:   strconv.FormatBool(config.File.Disable), log.EnvFileLevel: config.File.Level.String(),
		log.EnvFileFilterKeys: strings.Join(config.File.FilterKeys, ","), log.EnvFilePath: config.File.Path,
		log.EnvFileRotatingDisable:    strconv.FormatBool(config.File.Rotating.Disable),
		log.EnvFileRotatingMaxSize:    strconv.Itoa(config.File.Rotating.MaxSize),
		log.EnvFileRotatingMaxFileAge: strconv.Itoa(config.File.Rotating.MaxFileAge),
		log.EnvFileRotatingMaxFiles:   strconv.Itoa(config.File.Rotating.MaxFiles),
		log.EnvFileRotatingLocalTime:  strconv.FormatBool(config.File.Rotating.LocalTime),
		log.EnvFileRotatingCompress:   strconv.FormatBool(config.File.Rotating.Compress),
	}
	var restore []func() error
	restoreEnv := func() error {
		var result error
		for i := len(restore) - 1; i >= 0; i-- {
			result = errors.Join(result, restore[i]())
		}
		restore = nil
		return result
	}
	for key, value := range values {
		previous, exists := os.LookupEnv(key)
		restore = append(restore, func() error {
			if exists {
				return os.Setenv(key, previous)
			}
			return os.Unsetenv(key)
		})
		if err := os.Setenv(key, value); err != nil {
			return nil, nil, errors.Join(err, restoreEnv())
		}
	}
	logger, release, err := log.NewLogger()
	if err != nil {
		return nil, nil, errors.Join(err, restoreEnv())
	}
	return logger, func() {
		release()
		if err := restoreEnv(); err != nil {
			panic(err)
		}
	}, nil
}
