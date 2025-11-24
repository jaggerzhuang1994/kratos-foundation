package app_info

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"

	"os"
	"path/filepath"
)

func NewAppInfo(version string) *kratos_foundation_pb.AppInfo {
	id := fmt.Sprintf("%s-%s", Hostname, uuid.New().String())
	return &kratos_foundation_pb.AppInfo{
		Id:      id,
		Name:    execName(),
		Version: version,
		Metadata: map[string]string{
			MdEnv:      env.AppEnv(),
			MdHostname: Hostname,
		},
	}
}

type appInfoKey struct{}

func NewContext(ctx context.Context, appInfo *kratos_foundation_pb.AppInfo) context.Context {
	return context.WithValue(ctx, appInfoKey{}, appInfo)
}

func FromContext(ctx context.Context) (s *kratos_foundation_pb.AppInfo, ok bool) {
	s, ok = ctx.Value(appInfoKey{}).(*kratos_foundation_pb.AppInfo)
	return
}

func execName() string {
	path, err := os.Executable()
	if err != nil {
		panic(err)
	}
	_, exec := filepath.Split(path)
	return exec
}
