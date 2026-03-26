// Package server 提供服务器管理功能
//
// disable.go 实现了通过 Wire 依赖注入禁用 HTTP/GRPC 服务器的机制。
//
// 使用方式：
//
//	var ProviderSet = wire.NewSet(
//	    server.ProviderSet,
//	    server.NewDisableHttp,  // 禁用 HTTP 服务
//	    // 或
//	    server.NewDisableGrpc,  // 禁用 GRPC 服务
//	)
//
// 注意：这是一种编译时配置机制，不应在运行时动态使用。
package server

type DisableHttp any
type DisableGrpc any

type DisableState struct {
	disableHttp bool
	disableGrpc bool
}

func NewDisableState() *DisableState {
	return &DisableState{}
}

func NewDisableHttp(state *DisableState) DisableHttp {
	state.disableHttp = true
	return nil
}

func NewDisableGrpc(state *DisableState) DisableGrpc {
	state.disableGrpc = true
	return nil
}
