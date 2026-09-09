package wireassembly

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	_ "github.com/jaggerzhuang1994/kratos-foundation/v2/contrib/database/sqlite"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/database"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/pkg/server"
)

type order struct {
	ID     int64 `json:"id" gorm:"primaryKey"`
	Amount int64 `json:"amount"`
}

var errInvalidAmount = errors.New("order amount must be positive")

// 业务 fixture 只使用公开契约，Wire 必须先构造数据库才能注册端点。
func newServerSpec(manager database.Manager) *server.Spec {
	spec := server.NewSpec()
	spec.GRPC().Disable()
	spec.HTTP().Endpoint(func(srv server.HTTPServer) error {
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
			w.WriteHeader(http.StatusCreated)
		})
		return nil
	})
	return spec
}
