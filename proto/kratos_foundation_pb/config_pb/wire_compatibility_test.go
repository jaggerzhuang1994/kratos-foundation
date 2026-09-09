package config_pb

import (
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

// 直接构造 main 的 wire 数据，避免用当前描述符生成输入而掩盖字段编号回归。
func TestMainConfigWireCompatibility(t *testing.T) {
	field := func(number protowire.Number, value []byte) []byte {
		return protowire.AppendBytes(protowire.AppendTag(nil, number, protowire.BytesType), value)
	}
	t.Run("app", func(t *testing.T) {
		data := append(field(2, []byte{8, 10}), field(3, []byte{8, 30})...)
		var app App
		if err := proto.Unmarshal(data, &app); err != nil {
			t.Fatal(err)
		}
		if app.GetRegistrarTimeout().AsDuration() != 10*time.Second || app.GetStopTimeout().AsDuration() != 30*time.Second {
			t.Fatalf("old app decoded incorrectly: %v", &app)
		}
	})
	t.Run("server", func(t *testing.T) {
		data := append(field(3, field(3, []byte(":8000"))), field(4, field(3, []byte(":9000")))...)
		var server Server
		if err := proto.Unmarshal(data, &server); err != nil {
			t.Fatal(err)
		}
		if server.GetHttp().GetAddr() != ":8000" || server.GetGrpc().GetAddr() != ":9000" {
			t.Fatalf("old server decoded incorrectly: %v", &server)
		}
	})
	t.Run("database", func(t *testing.T) {
		entry := append(field(1, []byte("main")), field(2, field(2, []byte("database.db")))...)
		data := append(field(3, []byte("main")), field(4, entry)...)
		var database Database
		if err := proto.Unmarshal(data, &database); err != nil {
			t.Fatal(err)
		}
		if database.GetDefault() != "main" || database.GetConnections()["main"].GetDsn() != "database.db" {
			t.Fatalf("old database decoded incorrectly: %v", &database)
		}
	})
	t.Run("old timeout is not a deadline", func(t *testing.T) {
		var middleware ServerMiddleware
		if err := proto.Unmarshal(field(1, field(1, []byte{8, 2})), &middleware); err != nil {
			t.Fatal(err)
		}
		if middleware.GetDeadline() != nil {
			t.Fatalf("old timeout was reinterpreted: %v", &middleware)
		}
	})
}
