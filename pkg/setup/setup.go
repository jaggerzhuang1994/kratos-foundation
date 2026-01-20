package setup

import "github.com/go-kratos/kratos/v2/log"

func init() {
	log.SetLogger(log.With(log.GetLogger(),
		"ts", log.DefaultTimestamp,
		"caller", log.DefaultCaller,
	))
}
