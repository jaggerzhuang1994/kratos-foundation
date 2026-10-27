# observability

`Observability` 组合应用注入的日志、链路与指标 Provider，供需要观测依赖的组件共用。`queue.Observability` 与 `kafka.Observability` 是它的类型别名：

```go
obs := observability.New(logger, tracingProvider, metricsProvider) // 或 queue.NewObservability(...)
q, _ := queue.NewQueue(definition, store, obs)
consumer, _ := kafka.NewConsumerRuntime(config, rawConsumer, handler, obs)
box, _ := outbox.New("orders-outbox", store, obs)
```

它只借用 Provider，不创建资源、不读取全局实例，cleanup 仍由各 Provider 的拥有者负责。`bootstrap.BaseProviderSet` 已包含构造它的 provider，Wire 注入 `observability.Observability`、`queue.Observability` 或 `kafka.Observability` 得到的是同一个类型。
