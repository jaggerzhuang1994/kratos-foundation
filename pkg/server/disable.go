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
