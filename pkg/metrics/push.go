package metrics

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/appinfo"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/config"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	clientprometheus "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

// exportedJobLabel 承接指标自身的 job 标签；Pushgateway 分组键独占 job，
// 与 Prometheus honor_labels=false 时的冲突改名保持一致。
const exportedJobLabel = "exported_job"

// defaultPushConfig 只作为合并模板使用，Manager.Load 会先复制。
var defaultPushConfig = &config_pb.Metrics{Push: &config_pb.Push{Timeout: durationpb.New(5 * time.Second)}}

// Pusher 在一次性进程结束时把实例 Registry 推送到 Pushgateway。
// 配置在构造期读取一次；推送使用 PUT，替换同一 job 分组的上一次结果。
type Pusher struct {
	// url 不含用户信息的 Pushgateway 地址，错误信息会携带它。
	url string
	// job 分组键，默认应用名。
	job string
	// timeout 单次推送的超时预算。
	timeout time.Duration
	// username、password 从地址中剥离的 Basic Auth 凭据；hasAuth 为 false 时不设置。
	username, password string
	hasAuth            bool
	// gatherer 与 /metrics 抓取共享同一个实例 Registry。
	gatherer clientprometheus.Gatherer
}

// NewPusher 读取 metrics.push；未配置 url 时返回 nil，表示不推送。
// 地址或超时非法属于组装错误，返回 error 阻止应用启动，避免运行到结束才发现推送配置无效。
func NewPusher(manager config.Manager, provider Provider, appInfo appinfo.AppInfo) (*Pusher, error) {
	effective := new(config_pb.Metrics)
	if err := manager.Load("metrics", effective, defaultPushConfig); err != nil {
		return nil, err
	}
	pushConfig := effective.GetPush()
	if pushConfig.GetUrl() == "" {
		return nil, nil
	}
	timeout := pushConfig.GetTimeout()
	if err := timeout.CheckValid(); err != nil {
		return nil, fmt.Errorf("metrics push.timeout is invalid: %w", err)
	}
	if timeout.AsDuration() <= 0 {
		return nil, errors.New("metrics push.timeout must be positive")
	}
	raw := pushConfig.GetUrl()
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	target, err := url.Parse(raw)
	if err != nil || target.Host == "" || (target.Scheme != "http" && target.Scheme != "https") {
		// url.Parse 的错误会回显原始地址，可能包含凭据，因此只返回固定描述。
		return nil, errors.New("metrics push.url must be an absolute http(s) address")
	}
	pusher := &Pusher{
		job:      cmp.Or(pushConfig.GetJob(), appInfo.Name()),
		timeout:  timeout.AsDuration(),
		gatherer: provider.PrometheusGatherer(),
	}
	// 凭据转为 Basic Auth，保证推送失败时错误和日志中的地址不含密码。
	if target.User != nil {
		pusher.username = target.User.Username()
		pusher.password, _ = target.User.Password()
		pusher.hasAuth = true
		target.User = nil
	}
	pusher.url = target.String()
	return pusher, nil
}

// Job 返回 Pushgateway 分组使用的 job 名。
func (p *Pusher) Job() string { return p.job }

// Push 采集当前 Registry 并推送一次，受配置超时约束；调用方决定失败的处理方式。
func (p *Pusher) Push(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	// 超时由 Context 统一约束连接、发送与读取应答。
	pusher := push.New(p.url, p.job).Gatherer(jobLabelRenamer{p.gatherer})
	if p.hasAuth {
		pusher = pusher.BasicAuth(p.username, p.password)
	}
	if err := pusher.PushContext(ctx); err != nil {
		return fmt.Errorf("push metrics to pushgateway: %w", err)
	}
	return nil
}

// jobLabelRenamer 把指标自身的 job 标签（如 job_runs_total 的任务名）改为 exported_job。
// push 包拒绝含 job 标签的指标，直接推送会使所有包含任务指标的进程推送失败。
type jobLabelRenamer struct {
	clientprometheus.Gatherer
}

// Gather 返回改名后的指标；Registry 每次采集都构造新对象，原地修改不影响 /metrics 抓取。
func (g jobLabelRenamer) Gather() ([]*dto.MetricFamily, error) {
	families, err := g.Gatherer.Gather()
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			renamed := false
			for _, label := range metric.GetLabel() {
				if label.GetName() == "job" {
					label.Name = proto.String(exportedJobLabel)
					renamed = true
				}
			}
			if renamed {
				// 文本与 protobuf 编码都约定标签按名称排序，改名后重新排序。
				slices.SortFunc(metric.Label, func(a, b *dto.LabelPair) int { return strings.Compare(a.GetName(), b.GetName()) })
			}
		}
	}
	return families, err
}
