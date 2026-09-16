#!/bin/sh
# 创建独立 Docker 项目，验证完成或失败时只清理本次项目及其测试数据。
set -eu
# 功能验证可单独运行，未知参数在启动容器前拒绝。
functional_only=false
case "$#:$*" in
    0:) ;;
    1:--functional-only) functional_only=true ;;
    *) printf 'usage: %s [--functional-only]\n' "$0" >&2; exit 2 ;;
esac
root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$root"
project="foundation-validation-$(date +%Y%m%d%H%M%S)-$$"
output=${FOUNDATION_TEST_OUTPUT:-$(mktemp -d "${TMPDIR:-/tmp}/foundation-external.XXXXXX")}
mkdir -p "$output"
output=$(CDPATH= cd -- "$output" && pwd)
compose() { docker compose -p "$project" -f "$root/testdata/external/compose.yaml" "$@"; }
cleanup() {
    result=$?
    trap - EXIT HUP INT TERM
    compose logs --no-color > "$output/services.log" 2>&1 || true
    if ! compose down --volumes --remove-orphans > "$output/cleanup.log" 2>&1; then
        cat "$output/cleanup.log"
        result=1
    fi
    printf 'External test artifacts: %s\n' "$output"
    exit "$result"
}
trap cleanup EXIT
trap 'exit 130' HUP INT TERM
compose up -d --pull missing --wait --wait-timeout 150
export FOUNDATION_TEST_DOCKER_PROJECT="$project"
export FOUNDATION_TEST_KAFKA_ADDR=127.0.0.1:19092
export FOUNDATION_TEST_REDIS_ADDR=127.0.0.1:16379
export FOUNDATION_TEST_CONSUL_ADDR=127.0.0.1:18500
# 仅用于 compose 中的隔离数据库；不读取开发者现有 MySQL 的凭据。
export FOUNDATION_TEST_MYSQL_DSN='root:foundation-local-test-only@tcp(127.0.0.1:13306)/foundation_test?parseTime=true&timeout=2s&readTimeout=5s&writeTimeout=5s'
compose ps > "$output/services.txt"
go version > "$output/toolchain.txt"
if ! go test -race -count=1 -timeout=3m -run '^Test(External|StoreRedis|StatsRedis)' -v \
    ./pkg/kafka ./contrib/queue/redis ./contrib/database/mysql ./contrib/lock/redis ./pkg/redis ./contrib/config/consul \
    > "$output/functional.txt" 2>&1; then
    cat "$output/functional.txt"
    exit 1
fi
# 默认 GORM Repo 在相同隔离 MySQL 上验证泛型模型、事务与条件领取。
if ! FOUNDATION_TEST_QUEUE_MYSQL_DSN="$FOUNDATION_TEST_MYSQL_DSN" go test -race -count=1 -timeout=3m ./contrib/queue/database/gorm > "$output/queue-gorm.txt" 2>&1; then
    cat "$output/queue-gorm.txt"
    exit 1
fi
# 基准串行运行且不带 race/pprof，避免仪器开销污染批量比较。
if [ "$functional_only" = true ]; then
    printf 'External functional and race checks passed.\n'
    exit 0
fi
if ! go test ./pkg/kafka -run '^$' -bench '^BenchmarkExternalKafkaBatch$' \
    -benchtime=200x -count=6 -benchmem -timeout=6m > "$output/kafka-batch.txt" 2>&1; then
    cat "$output/kafka-batch.txt"
    exit 1
fi
# 单独收集剖析数据；它包含测试组装与释放，不能直接当成稳定处理路径占比。
if ! go test ./pkg/kafka -run '^$' -bench '^BenchmarkExternalKafkaBatch/128$' \
    -benchtime=200x -count=1 -timeout=2m -cpuprofile="$output/cpu.pprof" \
    -memprofile="$output/heap.pprof" -o "$output/kafka.test" > "$output/profile.txt" 2>&1; then
    cat "$output/profile.txt"
    exit 1
fi
go tool pprof -top "$output/cpu.pprof" > "$output/cpu-top.txt"
go tool pprof -top -alloc_space "$output/heap.pprof" > "$output/alloc-top.txt"
# ps 只返回本次 compose 项目中的容器 ID。
# shellcheck disable=SC2046
docker stats --no-stream --format '{{json .}}' $(compose ps -q) > "$output/container-stats.jsonl"
printf 'External functional, race, benchmark and profile checks passed.\n'
