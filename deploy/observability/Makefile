BLACKBOX_IMAGE := prom/blackbox-exporter:v0.28.0
PROMETHEUS_IMAGE := prom/prometheus:v3.5.5
COMPOSE := docker compose -f compose.yaml
PROMETHEUS_URL ?= http://127.0.0.1:19090

.PHONY: help check up down render-kubernetes smoke check-dashboards
help:
	@echo 'check: 校验 Compose、Prometheus 及告警测试，需要 Docker'
	@echo 'check-dashboards: 验证面板变量、四种视图和合成 ACK 数据的 PromQL，需要 Docker'
	@echo 'smoke: 对 PROMETHEUS_URL 验证真实 ACK 指标；可选 GRAFANA_URL 检查导入'
	@echo 'up: 构建并启动本地应用、Prometheus 和 Grafana'
	@echo 'down: 停止本示例，保留历史指标和 Grafana 数据卷'
	@echo 'render-kubernetes: 向 /tmp/foundation-monitoring 生成待审阅的 ConfigMap/PrometheusRule，不部署'

check:
	docker run --rm -v "$(CURDIR)/prometheus:/etc/blackbox:ro" $(BLACKBOX_IMAGE) --config.file=/etc/blackbox/blackbox.yaml --config.check
	$(COMPOSE) config --quiet
	docker run --rm --entrypoint promtool -v "$(CURDIR)/prometheus:/etc/prometheus:ro" $(PROMETHEUS_IMAGE) check config /etc/prometheus/prometheus.yaml
	docker run --rm --entrypoint promtool -v "$(CURDIR)/prometheus:/etc/prometheus:ro" $(PROMETHEUS_IMAGE) check config /etc/prometheus/standalone.yaml
	docker run --rm --entrypoint promtool -v "$(CURDIR)/prometheus:/etc/prometheus:ro" -w /etc/prometheus $(PROMETHEUS_IMAGE) test rules alerts.test.yaml

up:
	$(COMPOSE) up --build -d

down:
	$(COMPOSE) down

render-kubernetes:
	python3 render-kubernetes.py --namespace monitoring --release monitoring --output /tmp/foundation-monitoring

smoke:
	python3 smoke.py --prometheus-url "$(PROMETHEUS_URL)" $(if $(GRAFANA_URL),--grafana-url "$(GRAFANA_URL)",)

# 使用固定版本 promtool；不访问线上 Prometheus 或 Grafana。
check-dashboards:
	python3 check_dashboards.py $(if $(PROMTOOL_CONTAINER),--container $(PROMTOOL_CONTAINER),)
