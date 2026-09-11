package log

import (
	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log/internal/output"
	"time"
)

var SetLogger = kratoslog.SetLogger
var GetLogger = kratoslog.GetLogger
var Log = kratoslog.Log
var Context = kratoslog.Context
var Debug = kratoslog.Debug
var Debugf = kratoslog.Debugf
var Debugw = kratoslog.Debugw
var Info = kratoslog.Info
var Infof = kratoslog.Infof
var Infow = kratoslog.Infow
var Warn = kratoslog.Warn
var Warnf = kratoslog.Warnf
var Warnw = kratoslog.Warnw
var Error = kratoslog.Error
var Errorf = kratoslog.Errorf
var Errorw = kratoslog.Errorw
var Fatal = kratoslog.Fatal
var Fatalf = kratoslog.Fatalf
var Fatalw = kratoslog.Fatalw

// 未组装应用时使用标准输出，不打开文件、不持有需要关闭的资源。
// Bootstrap 安装 Wire Logger 后，全局日志使用应用输出；清理时恢复此默认值。
func init() {
	fallback := &logger{shared: processState, config: &configState{
		output: &outputLogger{output: output.NewStd()}, level: kratoslog.LevelInfo,
		filterEmpty: true,
		timeFormat:  time.RFC3339, msgKey: defaultMsgKey,
	}}
	kratoslog.SetLogger(fallback)
}
