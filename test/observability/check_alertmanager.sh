#!/usr/bin/env bash
# 校验 Alertmanager 示例配置与路由：critical 同时通知值班与群，占位告警静默，Job/机器告警进对应分组。
set -euo pipefail
image="${1:?alertmanager image}"
root="$(cd "$(dirname "$0")/../.." && pwd)"
amtool() { docker run --rm --entrypoint amtool -v "$root/observability/alertmanager:/etc/am:ro" "$image" "$@"; }
amtool check-config /etc/am/alertmanager.example.yaml > /dev/null
expect() {
    local want="$1"; shift
    local got
    got="$(amtool config routes test --config.file=/etc/am/alertmanager.example.yaml "$@")"
    if [ "$got" != "$want" ]; then
        echo "route $* -> $got, want $want" >&2
        exit 1
    fi
}
expect "pager,team-chat" alertname=FoundationAppUnavailable severity=critical app=api
expect "pager,team-chat" alertname=FoundationSLOBurnRateCritical severity=critical app=api
expect "team-chat" alertname=FoundationAppNotReady severity=warning app=api
expect "null" alertname=Watchdog severity=none
expect "null" alertname=KubeInfo severity=info
expect "team-chat" alertname=FoundationNodeCPUHigh severity=warning node=n1
expect "team-chat" alertname=FoundationJobFailed severity=warning owner_name=report
printf 'Alertmanager 示例配置与 %d 条路由断言通过\n' 7
