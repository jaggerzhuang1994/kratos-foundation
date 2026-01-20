package client

import (
	"errors"

	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
)

var ErrInvalidClientName = kratos_foundation_pb.ErrorInternalServer("client name is invalid")
var ErrInvalidGRPCClient = kratos_foundation_pb.ErrorInternalServer("invalid GRPC client")
var ErrInvalidHTTPClient = kratos_foundation_pb.ErrorInternalServer("invalid HTTP client")
var ErrInvalidProtocol = kratos_foundation_pb.ErrorInternalServer("invalid client protocol")

var ErrDiscoveryNotInitialized = kratos_foundation_pb.ErrorInternalServer("discovery not initialized")
var ErrParseTargetFailed = kratos_foundation_pb.ErrorInternalServer("parse target failed")

var ErrClientTimeout = kratos_foundation_pb.ErrorNetworkConnectTimeoutError("client: timeout")

// IsErrClientTimeout 是否为 ErrClientTimeout
func IsErrClientTimeout(err error) bool {
	return errors.Is(err, ErrClientTimeout)
}
