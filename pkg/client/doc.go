// Package client 提供按连接名称创建和管理 Kratos 客户端的公共入口。
//
// AcquireClient 显式接收连接名称，成功时返回调用级租约；调用方必须在本次调用结束时执行 release。
// release 可以幂等调用。遗漏 release 会让 Factory cleanup 持续等待该租约。
// 配置规范化、传输构造、版本租约和热更新实现按职责分文件保存在本包，
// 非导出类型隐藏内部状态；独立熔断中间件位于 internal/middleware/circuitbreaker。
package client
