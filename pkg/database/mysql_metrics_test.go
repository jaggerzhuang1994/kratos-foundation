package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestNewMySQLMetricsCollectorValidatesAndNormalizesConfiguration(t *testing.T) {
	db, _ := newMySQLMetricsScriptDB(t)
	logger := newManagerTestLogger(t)

	if collector, err := newMySQLMetricsCollector(nil, time.Second, nil, logger, prometheus.NewRegistry()); collector != nil || err == nil {
		t.Fatalf("nil DB constructor result = (%v, %v)", collector, err)
	}
	if collector, err := newMySQLMetricsCollector(db, time.Second, nil, nil, prometheus.NewRegistry()); collector != nil || err == nil {
		t.Fatalf("nil logger constructor result = (%v, %v)", collector, err)
	}

	configured, err := newMySQLMetricsCollector(db, time.Hour, &config_pb.GormMetrics_Mysql{
		Prefix:        proto.String(" mysql_status_ "),
		Interval:      durationpb.New(3 * time.Second),
		VariableNames: []string{" Threads_connected ", "Threads_connected", "Uptime"},
	}, logger, prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if configured.interval != 3*time.Second || configured.prefix != "mysql_status_" {
		t.Fatalf("normalized collector = interval %s prefix %q", configured.interval, configured.prefix)
	}
	if want := map[string]struct{}{"Threads_connected": {}, "Uptime": {}}; !reflect.DeepEqual(configured.variables, want) {
		t.Fatalf("normalized variables = %#v, want %#v", configured.variables, want)
	}

	fallback, err := newMySQLMetricsCollector(db, 0, nil, logger, prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	if fallback.interval != defaultMetricsRefreshInterval || fallback.prefix != defaultMySQLMetricsPrefix {
		t.Fatalf("fallback collector = interval %s prefix %q", fallback.interval, fallback.prefix)
	}

	for _, test := range []struct {
		name   string
		config *config_pb.GormMetrics_Mysql
		want   string
	}{
		{
			name:   "invalid prefix",
			config: &config_pb.GormMetrics_Mysql{Prefix: proto.String("bad-prefix-")},
			want:   "prefix",
		},
		{
			name:   "empty variable",
			config: &config_pb.GormMetrics_Mysql{VariableNames: []string{" "}},
			want:   "variable name is empty",
		},
		{
			name:   "invalid variable",
			config: &config_pb.GormMetrics_Mysql{VariableNames: []string{"bad-name"}},
			want:   "variable name",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			collector, err := newMySQLMetricsCollector(db, time.Second, test.config, logger, prometheus.NewRegistry())
			if collector != nil || err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("constructor result = (%v, %v), want error containing %q", collector, err, test.want)
			}
		})
	}
}

func TestMySQLMetricsCollectorRegistersStableDescriptorsAndDropsMissingValues(t *testing.T) {
	db, _ := newMySQLMetricsScriptDB(t,
		mysqlMetricsQueryStep{rows: [][]driver.Value{{"Uptime", "42"}, {"Threads_connected", "7"}}},
		mysqlMetricsQueryStep{rows: [][]driver.Value{{"Questions", "9"}}},
	)
	registry := prometheus.NewPedanticRegistry()
	collector, err := newMySQLMetricsCollector(db, time.Second, &config_pb.GormMetrics_Mysql{Prefix: proto.String("mysql_")}, newManagerTestLogger(t), registry)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(collector.close)
	for _, want := range []map[string]float64{{"mysql_Uptime": 42, "mysql_Threads_connected": 7}, {"mysql_Questions": 9}} {
		collector.refresh(context.Background())
		families, err := registry.Gather()
		if err != nil {
			t.Fatal(err)
		}
		got := make(map[string]float64)
		for _, family := range families {
			got[family.GetName()] = family.Metric[0].Gauge.GetValue()
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Gather() = %#v, want %#v", got, want)
		}
	}
	collector.close()
	if families, err := registry.Gather(); err != nil || len(families) != 0 {
		t.Fatalf("after cleanup families=%d err=%v", len(families), err)
	}
}

func TestMySQLMetricsCollectorRefreshReplacesOnlyCompleteSuccessfulSnapshots(t *testing.T) {
	queryFailure := errors.New("query failed")
	rowsFailure := errors.New("rows failed")
	db, script := newMySQLMetricsScriptDB(t,
		mysqlMetricsQueryStep{rows: [][]driver.Value{
			{"Threads_connected", "5"},
			{"Uptime", "12.5"},
			{"bad-name", "99"},
			{"Questions", "not-a-number"},
		}},
		mysqlMetricsQueryStep{queryErr: queryFailure},
		mysqlMetricsQueryStep{rows: [][]driver.Value{{nil, "6"}}},
		mysqlMetricsQueryStep{rows: [][]driver.Value{{"Threads_connected", "6"}}, rowsErr: rowsFailure},
		mysqlMetricsQueryStep{rows: [][]driver.Value{{"Uptime", "20"}}},
	)
	collector, err := newMySQLMetricsCollector(db, time.Second, &config_pb.GormMetrics_Mysql{
		Prefix: proto.String("mysql_"),
	}, newManagerTestLogger(t), prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}

	collector.refresh(context.Background())
	wantInitial := map[string]float64{"Threads_connected": 5, "Uptime": 12.5}
	assertMySQLMetricsValues(t, collector, wantInitial)

	for _, failure := range []string{"query", "scan", "rows"} {
		collector.refresh(context.Background())
		assertMySQLMetricsValues(t, collector, wantInitial)
		t.Logf("%s failure retained the previous complete snapshot", failure)
	}

	collector.refresh(context.Background())
	assertMySQLMetricsValues(t, collector, map[string]float64{"Uptime": 20})
	if queries := script.querySnapshot(); len(queries) != 5 {
		t.Fatalf("queries = %#v", queries)
	} else {
		for _, query := range queries {
			if query != "SHOW STATUS" {
				t.Fatalf("query = %q, want SHOW STATUS", query)
			}
		}
	}
}

func TestMySQLMetricsCollectorRefreshAppliesExactWhitelistAndNumericParsing(t *testing.T) {
	db, _ := newMySQLMetricsScriptDB(t, mysqlMetricsQueryStep{rows: [][]driver.Value{
		{"Threads_connected", "8"},
		{" threads_connected ", "9"},
		{"Uptime", "100"},
		{"Threads_connected", "invalid"},
	}})
	collector, err := newMySQLMetricsCollector(db, time.Second, &config_pb.GormMetrics_Mysql{
		VariableNames: []string{" Threads_connected "},
	}, newManagerTestLogger(t), prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	collector.refresh(context.Background())
	// The later malformed duplicate must not erase the last successfully parsed value.
	assertMySQLMetricsValues(t, collector, map[string]float64{"Threads_connected": 8})
}

func TestMySQLMetricsCollectorStartRunsImmediatelyRefreshesPeriodicallyAndStops(t *testing.T) {
	db, script := newMySQLMetricsScriptDB(t,
		mysqlMetricsQueryStep{rows: [][]driver.Value{{"Uptime", "1"}}},
		mysqlMetricsQueryStep{rows: [][]driver.Value{{"Uptime", "2"}}},
		mysqlMetricsQueryStep{waitForCancel: true},
	)
	collector, err := newMySQLMetricsCollector(db, 5*time.Millisecond, nil, newManagerTestLogger(t), prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}

	collector.start()
	for attempt := 1; attempt <= 3; attempt++ {
		if query := receiveMySQLMetricsQuery(t, script.calls); query != "SHOW STATUS" {
			t.Fatalf("query %d = %q", attempt, query)
		}
	}
	assertMySQLMetricsValues(t, collector, map[string]float64{"Uptime": 2})

	closed := make(chan struct{})
	go func() {
		collector.close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("collector close did not cancel the in-flight refresh")
	}
	collector.close()
	select {
	case query := <-script.calls:
		t.Fatalf("collector queried again after close: %q", query)
	case <-time.After(20 * time.Millisecond):
	}

	var nilCollector *mysqlMetricsCollector
	nilCollector.close()
	(&mysqlMetricsCollector{}).close()
}

func assertMySQLMetricsValues(
	t testing.TB,
	collector *mysqlMetricsCollector,
	want map[string]float64,
) {
	t.Helper()
	collector.mu.RLock()
	got := make(map[string]float64, len(collector.values))
	maps.Copy(got, collector.values)
	collector.mu.RUnlock()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collector values = %#v, want %#v", got, want)
	}
}

type mysqlMetricsQueryStep struct {
	rows          [][]driver.Value
	queryErr      error
	rowsErr       error
	waitForCancel bool
}

type mysqlMetricsScriptDriver struct {
	mu      sync.Mutex
	steps   []mysqlMetricsQueryStep
	queries []string
	calls   chan string
}

var mysqlMetricsDriverID atomic.Uint64

func newMySQLMetricsScriptDB(
	t testing.TB,
	steps ...mysqlMetricsQueryStep,
) (*sql.DB, *mysqlMetricsScriptDriver) {
	t.Helper()
	script := &mysqlMetricsScriptDriver{
		steps: append([]mysqlMetricsQueryStep(nil), steps...),
		calls: make(chan string, 32),
	}
	driverName := fmt.Sprintf("foundation_mysql_metrics_test_%d", mysqlMetricsDriverID.Add(1))
	sql.Register(driverName, script)
	db, err := sql.Open(driverName, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close scripted SQL DB: %v", err)
		}
	})
	return db, script
}

func (driver *mysqlMetricsScriptDriver) Open(string) (driver.Conn, error) {
	return &mysqlMetricsScriptConn{driver: driver}, nil
}

func (driver *mysqlMetricsScriptDriver) next(query string) (mysqlMetricsQueryStep, bool) {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	driver.queries = append(driver.queries, query)
	if len(driver.steps) == 0 {
		return mysqlMetricsQueryStep{}, false
	}
	step := driver.steps[0]
	driver.steps = driver.steps[1:]
	return step, true
}

func (driver *mysqlMetricsScriptDriver) querySnapshot() []string {
	driver.mu.Lock()
	defer driver.mu.Unlock()
	return append([]string(nil), driver.queries...)
}

type mysqlMetricsScriptConn struct {
	driver *mysqlMetricsScriptDriver
}

func (*mysqlMetricsScriptConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("scripted MySQL metrics driver does not prepare statements")
}

func (*mysqlMetricsScriptConn) Close() error { return nil }

func (*mysqlMetricsScriptConn) Begin() (driver.Tx, error) {
	return nil, errors.New("scripted MySQL metrics driver does not begin transactions")
}

func (connection *mysqlMetricsScriptConn) QueryContext(
	ctx context.Context,
	query string,
	_ []driver.NamedValue,
) (driver.Rows, error) {
	step, ok := connection.driver.next(query)
	connection.driver.calls <- query
	if !ok {
		return nil, errors.New("unexpected scripted MySQL metrics query")
	}
	if step.waitForCancel {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if step.queryErr != nil {
		return nil, step.queryErr
	}
	return &mysqlMetricsScriptRows{rows: step.rows, terminalErr: step.rowsErr}, nil
}

type mysqlMetricsScriptRows struct {
	rows             [][]driver.Value
	index            int
	terminalErr      error
	terminalReturned bool
}

func (*mysqlMetricsScriptRows) Columns() []string {
	return []string{"Variable_name", "Value"}
}

func (*mysqlMetricsScriptRows) Close() error { return nil }

func (rows *mysqlMetricsScriptRows) Next(destination []driver.Value) error {
	if rows.index < len(rows.rows) {
		copy(destination, rows.rows[rows.index])
		rows.index++
		return nil
	}
	if rows.terminalErr != nil && !rows.terminalReturned {
		rows.terminalReturned = true
		return rows.terminalErr
	}
	return io.EOF
}

func receiveMySQLMetricsQuery(t testing.TB, calls <-chan string) string {
	t.Helper()
	select {
	case query := <-calls:
		return query
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for MySQL metrics query")
		return ""
	}
}

var _ driver.Driver = (*mysqlMetricsScriptDriver)(nil)
var _ driver.QueryerContext = (*mysqlMetricsScriptConn)(nil)
