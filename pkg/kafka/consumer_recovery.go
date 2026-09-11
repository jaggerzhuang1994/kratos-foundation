package kafka

import (
	"context"
	"errors"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/internal/reconnect"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

// consumerOperationError 只标记 Kafka 操作失败，避免把 Handler 错误当作可恢复网络故障。
type consumerOperationError struct{ err error }

func (e *consumerOperationError) Error() string { return e.err.Error() }
func (e *consumerOperationError) Unwrap() error { return e.err }

func recoverableConsumerOperation(err error, allowFirstRead bool) bool {
	var operation *consumerOperationError
	return errors.As(err, &operation) && transientKafkaError(operation.err, allowFirstRead)
}

// transientKafkaError 保留 SDK 的错误分类；失去组代次后通过新客户端重新入组。
// 聚合错误中只要有一个永久失败，就不能用重连掩盖它。
func transientKafkaError(err error, allowFirstRead bool) bool {
	if err == nil {
		return false
	}
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		if joined, ok := cause.(interface{ Unwrap() []error }); ok {
			hasError := false
			for _, child := range joined.Unwrap() {
				if child == nil {
					continue
				}
				hasError = true
				if !transientKafkaError(child, allowFirstRead) {
					return false
				}
			}
			return hasError
		}
	}
	var firstRead *kgo.ErrFirstReadEOF
	if errors.Is(err, context.Canceled) || errors.Is(err, kgo.ErrClientClosed) {
		return false
	}
	if errors.As(err, &firstRead) {
		return allowFirstRead
	}
	return kgo.IsRetryableBrokerErr(err) || kerr.IsRetriable(err) || reconnect.Transient(err) ||
		errors.Is(err, kerr.IllegalGeneration) || errors.Is(err, kerr.UnknownMemberID) ||
		errors.Is(err, kerr.RebalanceInProgress) || errors.Is(err, kerr.FencedMemberEpoch)
}
