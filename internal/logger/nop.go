package internal_logger

import "github.com/go-kratos/kratos/v2/log"

type nopLogger struct{}

func NewNopLogger() log.Logger {
	return &nopLogger{}
}

func (*nopLogger) Log(_ log.Level, _ ...any) error {
	return nil
}
