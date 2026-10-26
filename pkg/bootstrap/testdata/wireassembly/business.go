package wireassembly

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"

	kratoshttp "github.com/go-kratos/kratos/v2/transport/http"
	_ "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/database/sqlite"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/log"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
)

type order struct {
	ID     int64 `json:"id" gorm:"primaryKey"`
	Amount int64 `json:"amount"`
}

var errInvalidAmount = errors.New("order amount must be positive")

// 业务 fixture 只使用公开契约，Wire 必须先构造数据库才能注册端点。
type businessServer struct {
	manager database.Manager
	http    server.HTTPServer
	// daemonStartedReady 记录常驻任务启动时应用是否已经 Ready；daemonStopped 记录其收到取消。
	daemonStarted, daemonStartedReady, daemonStopped atomic.Bool
}

// runDaemon 是组合场景中的常驻任务：启动时记录 Ready 状态，阻塞到停机取消。
func (b *businessServer) runDaemon(ctx context.Context, ready func() bool) error {
	b.daemonStartedReady.Store(ready())
	b.daemonStarted.Store(true)
	<-ctx.Done()
	b.daemonStopped.Store(true)
	return ctx.Err()
}

func newBusinessServer(manager database.Manager) *businessServer {
	return &businessServer{manager: manager}
}

func (b *businessServer) register(srv server.HTTPServer) error {
	b.http = srv
	manager := b.manager
	// /fail 经 Kratos 路由返回未知错误，由 Foundation Encoder 归一化为 500，用于验证诊断出口。
	srv.Route("/").GET("/fail", func(kratoshttp.Context) error {
		return errors.New("query failed: password=private-cause")
	})
	srv.HandleFunc("/orders", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			order
			Connection string `json:"connection"`
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1024)
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid order", http.StatusBadRequest)
			return
		}
		ctx := r.Context()
		if request.Connection != "" {
			ctx = database.UseConnection(ctx, request.Connection)
		}
		err := manager.Transaction(ctx, func(txCtx context.Context) error {
			if err := manager.Connection(txCtx).Create(&request.order).Error; err != nil {
				return err
			}
			// 模拟写入后的业务拒绝，验证错误能够回滚已经执行的 SQL。
			if request.Amount <= 0 {
				return errInvalidAmount
			}
			return nil
		})
		if errors.Is(err, errInvalidAmount) {
			http.Error(w, "invalid amount", http.StatusUnprocessableEntity)
			return
		}
		if err != nil {
			http.Error(w, "order failed", http.StatusInternalServerError)
			return
		}
		log.WithContext(ctx).With("event", "demo.order.created", "order.id", request.ID).Info("order created") // compose:caller
		w.WriteHeader(http.StatusCreated)
	})
	return nil

}
