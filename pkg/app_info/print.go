package app_info

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jaggerzhuang1994/kratos-foundation/pkg/env"
	"github.com/jaggerzhuang1994/kratos-foundation/pkg/utils"
	"github.com/jaggerzhuang1994/kratos-foundation/proto/kratos_foundation_pb"
)

func PrintAppInfo(ai *kratos_foundation_pb.AppInfo) {
	md := fmt.Sprintf("%#v", ai.GetMetadata())
	md = "map" + md[17:]

	debug := fmt.Sprintf("%v", env.AppDebug())
	appEnv := env.AppEnv()

	w := utils.Max(
		len(ai.GetId()),
		len(debug),
		len(appEnv),
		len(ai.GetName()),
		len(ai.GetVersion()),
		len(md),
	)

	delta := 14
	divider := strings.Repeat("-", w+delta)

	fmt.Printf(`%s
|%sAppInfo%s|
%s
| ID      | %-`+strconv.Itoa(w)+`s |
| Name    | %-`+strconv.Itoa(w)+`s |
| Version | %-`+strconv.Itoa(w)+`s |
| MD      | %-`+strconv.Itoa(w)+`s |
| DEBUG   | %-`+strconv.Itoa(w)+`s |
| Env     | %-`+strconv.Itoa(w)+`s |
%s
`,
		divider,
		strings.Repeat(" ", (w+delta)/2-3),
		strings.Repeat(" ", (w+delta)-(w+delta)/2-6),
		divider,
		ai.GetId(),
		ai.GetName(),
		ai.GetVersion(),
		md,
		debug,
		appEnv,
		divider,
	)
}
