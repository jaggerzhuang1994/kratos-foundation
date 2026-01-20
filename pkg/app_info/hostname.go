package app_info

import (
	"os"
)

// Hostname 主机名，在包初始化时自动获取
var Hostname string

// init 包初始化函数
// 自动获取主机名和可执行文件名称，失败时 panic
func init() {
	var err error
	Hostname, err = os.Hostname()
	if err != nil {
		panic(err)
	}
}
