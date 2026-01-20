package app_info

import (
	"os"
	"path/filepath"
)

// ExecName 可执行文件名称，在包初始化时自动获取
var ExecName string

// init 包初始化函数
// 自动获取主机名和可执行文件名称，失败时 panic
func init() {
	path, err := os.Executable()
	if err != nil {
		panic(err)
	}
	_, ExecName = filepath.Split(path)
}
