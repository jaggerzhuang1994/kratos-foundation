package websocket

type Hook struct {
	server []func(*Server)
}

func NewHook() *Hook {
	return &Hook{}
}

func (h *Hook) WebsocketServer(fn func(*Server)) {
	h.server = append(h.server, fn)
}
