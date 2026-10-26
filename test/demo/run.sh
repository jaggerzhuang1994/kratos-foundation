#!/bin/sh
# 本地 kind 集群监控 Demo：部署 kube-prometheus-stack、中间件、多角色业务应用与仓库面板/规则。
# 用法：test/demo/run.sh up | down | reload（只重新构建并部署应用）。
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
export PATH="$root/tools:$PATH"
cluster=foundation-demo
context=kind-$cluster
kubectl="kubectl --context $context"
chart_version=92.13.1

ensure_tools() {
    command -v kind >/dev/null || GOBIN="$root/tools" go install sigs.k8s.io/kind@v0.33.0
    command -v helm >/dev/null || GOBIN="$root/tools" go install helm.sh/helm/v3/cmd/helm@v3.22.1
}

# 宿主机代理若指向 127.0.0.1，节点容器内不可达；改写为 host.docker.internal 供镜像拉取使用。
node_proxy() {
    printf '%s' "${1:-}" | sed 's#127\.0\.0\.1#host.docker.internal#; s#localhost#host.docker.internal#'
}

create_cluster() {
    kind get clusters 2>/dev/null | grep -qx "$cluster" && return
    HTTP_PROXY=$(node_proxy "${HTTP_PROXY:-}") HTTPS_PROXY=$(node_proxy "${HTTPS_PROXY:-}") \
        NO_PROXY="${NO_PROXY:-localhost,127.0.0.1},.svc,.cluster.local" \
        kind create cluster --config test/demo/kind.yaml --wait 180s
}

install_monitoring() {
    probes=$(mktemp)
    # 复用仓库的健康探测配置，只替换 namespace、应用筛选与集群名。
    sed -e 's/names: \[foundation-demo\]/names: [shop]/' -e 's/regex: minimal-api/regex: .+/' \
        -e 's/example-cluster/kind-demo/' observability/kubernetes/health-values.yaml > "$probes"
    helm repo add prometheus-community https://prometheus-community.github.io/helm-charts >/dev/null 2>&1 || true
    helm upgrade --install monitoring prometheus-community/kube-prometheus-stack --version "$chart_version" \
        --kube-context "$context" -n monitoring --create-namespace \
        -f test/demo/monitoring-values.yaml -f "$probes" --wait --timeout 15m
    rm -f "$probes"
    $kubectl apply -f observability/kubernetes/blackbox.yaml
}

build_app() {
    out=$(mktemp -d)
    CGO_ENABLED=0 GOOS=linux GOARCH=$(go env GOARCH) go build -tags demo -trimpath -o "$out/demo" ./test/demo/cmd/demo
    cp test/demo/Dockerfile "$out/Dockerfile"
    docker build -q -t foundation-demo:dev "$out" >/dev/null
    rm -rf "$out"
    kind load docker-image foundation-demo:dev --name "$cluster"
}

deploy_apps() {
    $kubectl apply -f test/demo/k8s/middleware.yaml
    $kubectl -n middleware rollout status deploy/mysql deploy/redis deploy/kafka --timeout=300s
    $kubectl -n middleware wait --for=condition=complete job/kafka-topics --timeout=300s
    $kubectl apply -f test/demo/k8s/apps.yaml
    $kubectl -n shop create configmap shop-config --from-file=config.yaml=test/demo/config.yaml --dry-run=client -o yaml | $kubectl apply -f -
    $kubectl -n shop rollout restart deploy
}

load_dashboards() {
    python3 observability/kubernetes/render.py --namespace monitoring --release monitoring --output /tmp/foundation-demo-monitoring >/dev/null
    # 面板 ConfigMap 超过客户端 apply 的 256KiB 注解上限，与接入文档一致使用 server-side apply。
    $kubectl apply --server-side --force-conflicts -f /tmp/foundation-demo-monitoring/dashboard.json
    $kubectl apply -f /tmp/foundation-demo-monitoring/rules.yaml
}

case "${1:-}" in
    up)
        ensure_tools; create_cluster; install_monitoring; build_app; deploy_apps; load_dashboards
        echo "Grafana: http://127.0.0.1:13300 (admin / demo-local-only)  Prometheus: http://127.0.0.1:19900"
        ;;
    reload) build_app; deploy_apps; load_dashboards ;;
    down) kind delete cluster --name "$cluster" ;;
    *) echo "usage: $0 up|reload|down" >&2; exit 2 ;;
esac
