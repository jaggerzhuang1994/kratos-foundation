BLACKBOX_IMAGE := prom/blackbox-exporter:v0.28.0
PROMETHEUS_IMAGE := prom/prometheus:v3.5.5
COMPOSE := docker compose -f compose.yaml

.PHONY: help check up down render-kubernetes smoke
help:
	@echo 'check: 校验 Compose、Prometheus 及告警测试，需要 Docker'
	@echo 'smoke: 验证已启动的本地 HTTP、采集标签、Grafana 和所有聚合查询'
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
	python3 smoke.py
