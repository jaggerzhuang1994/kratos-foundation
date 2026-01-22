//go:generate sh -c "go run export_package.go > ../export.go 2>&2"
package main

import (
	"fmt"

	"github.com/jaggerzhuang1994/kratos-foundation/internal/export_package"
)

func main() {
	fmt.Println(export_package.ExportPackage("github.com/gorilla/websocket", "websocket"))
}
