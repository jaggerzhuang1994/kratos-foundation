package common

import "time"

type logger interface {
	Debugf(format string, a ...any)
	Infof(format string, a ...any)
}

// TimeRecord 用于快速记录多段任务的耗时，避免重复写日志逻辑。
type TimeRecord struct {
	logger

	t time.Time
}

// NewTimeRecord 返回新的耗时记录器，并立即记录起始时间。
func NewTimeRecord(logger logger) *TimeRecord {
	return &TimeRecord{logger: logger, t: time.Now()}
}

// Reset 将计时起点重置为当前时间，通常用于跨阶段统计。
func (t *TimeRecord) Reset() {
	t.t = time.Now()
}

// Record 记录任务耗时到 Debug 日志，适合频繁执行的步骤。
func (t *TimeRecord) Record(task string) {
	t.Debugf("%s duration: %s", task, time.Since(t.t))
	t.t = time.Now()
}

// RecordInfo 记录任务耗时到 Info 日志，适合关键步骤。
func (t *TimeRecord) RecordInfo(task string) {
	t.Infof("%s duration: %s", task, time.Since(t.t))
	t.t = time.Now()
}
