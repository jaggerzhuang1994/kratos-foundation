package app_info

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
)

// Version 需要应用自行注入 Version
type Version string

// AppInfo 应用信息接口
// 定义应用基本元数据的访问方法
type AppInfo interface {
	GetId() string                  // 获取应用唯一标识
	GetName() string                // 获取应用名称
	GetVersion() string             // 获取应用版本
	GetMetadata() map[string]string // 获取应用元数据
}

// NewAppInfo 创建应用信息实例
// 自动生成唯一 ID，并包含主机名、环境等元数据
func NewAppInfo(
	version Version,
) AppInfo {
	id := fmt.Sprintf("%s-%s", Hostname, uuid.New().String())
	ai := &appInfo{
		&kratos_foundation_pb.AppInfo{
			Id:      id,
			Name:    ExecName,
			Version: string(version),
			Metadata: map[string]string{
				MdEnv:      env.AppEnv(), // 环境信息
				MdHostname: Hostname,     // 主机名
			},
		},
	}
	ai.print()
	return ai
}

type appInfo struct {
	*kratos_foundation_pb.AppInfo
}

// print 格式化打印应用信息
// 输出一个包含应用 ID、名称、版本、元数据、调试模式和环境信息的表格
func (ai *appInfo) print() {
	md := fmt.Sprintf("%#v", ai.GetMetadata())
	md = "map" + md[17:]

	debug := fmt.Sprintf("%v", env.AppDebug())
	appEnv := env.AppEnv()

	w := utils.Max(
		len(ai.GetId()),
		len(debug),
		len(appEnv),
		len(ai.GetName()),
		len(ai.GetVersion()),
		len(md),
	)

	delta := 14
	divider := strings.Repeat("-", w+delta)

	fmt.Printf(`%s
|%sAppInfo%s|
%s
| ID       | %-`+strconv.Itoa(w)+`s |
| Name     | %-`+strconv.Itoa(w)+`s |
| Version  | %-`+strconv.Itoa(w)+`s |
| Metadata | %-`+strconv.Itoa(w)+`s |
| Debug    | %-`+strconv.Itoa(w)+`s |
| Env      | %-`+strconv.Itoa(w)+`s |
%s
`,
		divider,
		strings.Repeat(" ", (w+delta)/2-3),
		strings.Repeat(" ", (w+delta)-(w+delta)/2-6),
		divider,
		ai.GetId(),
		ai.GetName(),
		ai.GetVersion(),
		md,
		debug,
		appEnv,
		divider,
	)
}
