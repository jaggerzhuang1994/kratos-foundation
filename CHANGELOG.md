# Changelog

本文件记录面向调用方的版本变化。版本是否已经发布以 Git tag 和对应提交为准；“计划中”内容不能作为已发布能力依赖。

## v2.1.0（计划中，尚未发布）

### Added

- Queue 新增可选 `Operations` 契约，提供元数据分页、详情、条件删除/取消/重试及有界保留期清理；不内置统一管理 API 或 UI。
- Job 新增触发跳过、当前等待数和等待耗时指标。
- Log 新增 `AtLevel` / `DebugOnly` 条件字段；`DebugOnly` 同时响应请求级 debug，可让同一条 Info 访问日志按请求展开诊断字段。
- Server 在启动前逐条记录最终业务 HTTP、gRPC 和 metrics/health 注册端点；Job 和 Queue 增加统一的执行开始/结束事件与耗时字段。
- 建立变更记录，并明确平台终止 TLS/mTLS、Foundation 内部应用链路使用明文的信任边界。

### Changed

- JSON Schema 外部合并保留根级约束；同名 definition 仅在 JSON 语义相同时接受，冲突时停止生成。
- MySQL 服务端状态指标改由独立 `mysqld-exporter` 等基础设施采集器负责；Database Manager 只保留应用连接池和 SQL 操作指标。
- Bootstrap 登记的 Job Runtime 改为等待应用 Ready 后启动，并在启动任务前记录名称、最终调度规则和注册调用点。
- GORM Queue 以 `Task.ID` 作为唯一公开主键，MySQL/SQLite 分别用 VARBINARY/BLOB 保留逐字节身份，内部 `generation` 仅用于防止 ABA；任务正文不再重复保存 ID/AvailableAt，三个任务时间字段改为 SQL 时间列并校验 MySQL TIMESTAMP 范围。
- 请求日志按摘要与诊断字段分层：deadline 细节、完整错误链和 panic 堆栈仅在 Debug 事件或请求级 debug 中展开，常规故障日志仍保留稳定分类与紧凑错误摘要。

### Removed

- 删除应用进程内基于 `SHOW STATUS` 的 MySQL 状态轮询及其 `database.metrics.mysql` 配置。

### Migration

- 删除部署配置中的 `database.metrics.mysql`；如需 MySQL 全局状态与 InnoDB 指标，在平台监控层部署并授权 `mysqld-exporter`。
- Queue 运维入口必须由业务自行实现鉴权、审计、脱敏和限流；`List` 不返回正文，正文仅由 `Get` 获取。
- 应用业务端口与管理端口均不可直接暴露到不可信网络；TLS/mTLS 在平台层完成。
- v2.1 的 GORM Queue 表不兼容旧主键、`identity` 和整数时间列；升级时停止旧 Worker，清空并按当前模型重建任务表，不提供旧任务原地迁移。
