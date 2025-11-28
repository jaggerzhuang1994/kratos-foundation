package setup

import (
	"time"

	"github.com/go-kratos/kratos/v2/log"
)

func init() {
	log.SetLogger(log.With(log.GetLogger(), "ts", log.Timestamp(time.RFC3339)))
}
