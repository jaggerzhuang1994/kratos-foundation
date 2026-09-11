#!/usr/bin/env python3
"""验证已启动的本地 Compose：真实请求、采集身份、Grafana 导入和全部聚合查询。"""
import json
import re
import time
import urllib.parse
import urllib.request
from pathlib import Path

# 有界业务请求提供真实样本，不生成模拟监控数据。
for _ in range(10):
    with urllib.request.urlopen("http://127.0.0.1:18000/hello", timeout=3) as response:
        assert response.status == 200
with urllib.request.urlopen("http://127.0.0.1:19001/readyz", timeout=3) as response:
    assert response.status == 200
dashboards = []
for path in sorted((Path(__file__).parent / "grafana/dashboards").glob("*.json")):
    source = json.loads(path.read_text())
    with urllib.request.urlopen("http://127.0.0.1:13000/api/dashboards/uid/"+source["uid"],timeout=5) as response:
        dashboard = json.load(response)["dashboard"]
    assert dashboard["panels"] == source["panels"], "Grafana 未加载当前面板: "+source["uid"]
    assert all(not p.get("collapsed",False) for p in source["panels"] if p["type"]=="row")
    assert len({p["id"] for p in source["panels"]})==len(source["panels"])
    for p in source["panels"]:
        unit = p.get("fieldConfig",{}).get("defaults",{}).get("unit","")
        if unit == "percentunit":
            defaults = p["fieldConfig"]["defaults"]
            assert defaults.get("min") == 0 and defaults.get("max") == 1, p["title"]
        if p["type"] != "row" and any(word in p["title"] for word in ["P95","P99","耗时","平均等待时间"]):
            assert unit in ("s","ms"), (p["title"],unit)
    dashboards.append(dashboard)
    print("Grafana provisioned dashboard:",dashboard["title"])
with urllib.request.urlopen("http://127.0.0.1:19090/api/v1/targets", timeout=5) as response:
    targets = json.load(response)["data"]["activeTargets"]
assert targets, "Prometheus 没有发现应用目标"
for target in targets:
    if target["labels"].get("foundation") != "true" and target["labels"].get("foundation_probe") != "true":
        continue
    assert target["health"] == "up", target["lastError"]
    assert all(
        name in target["labels"]
        for name in ["env", "cluster", "namespace", "app", "node", "pod", "instance"]
    )
    print("target labels", target["labels"])

deadline = time.monotonic() + 45
while True:
    query = urllib.parse.urlencode(
        {"query": 'rate(server_requests_code_total{foundation="true"}[1m])'}
    )
    with urllib.request.urlopen(
        "http://127.0.0.1:19090/api/v1/query?" + query, timeout=5
    ) as response:
        ready = json.load(response)["data"]["result"]
    if ready:
        break
    if time.monotonic() >= deadline:
        raise RuntimeError("45 秒内未获得两次有效请求指标采样，请检查 Prometheus Targets")
    time.sleep(1)

def panels(items):
    for panel in items:
        yield panel
        yield from panels(panel.get("panels", []))


# 默认示例必须真的有运行时、配置与两条健康探测样本，不能只验证空查询语法。
for metric_name in ["go_cpu_classes_gc_total_cpu_seconds_total", "go_sched_latencies_seconds_count", "process_virtual_memory_bytes", "foundation_config_watcher_up"]:
    query = urllib.parse.urlencode({"query": metric_name + '{foundation="true"}'})
    with urllib.request.urlopen("http://127.0.0.1:19090/api/v1/query?" + query, timeout=5) as response:
        samples = json.load(response)["data"]["result"]
    assert samples and all(sample["metric"].get("target") for sample in samples), metric_name
query = urllib.parse.urlencode({"query": 'probe_success{foundation_probe="true"}'})
with urllib.request.urlopen("http://127.0.0.1:19090/api/v1/query?" + query, timeout=5) as response:
    probes = json.load(response)["data"]["result"]
assert {sample["metric"]["probe"] for sample in probes} == {"healthz", "readyz"}
assert all(float(sample["value"][1]) == 1 and sample["metric"].get("target") for sample in probes)
print("runtime, config, readiness and liveness samples verified")

count = 0
for group in ["app", "pod", "instance", "node", "target"]:
    for panel in (p for dashboard in dashboards for p in panels(dashboard["panels"])):
        for target in panel.get("targets", []):
            expression = target["expr"].replace("${group_by:raw}", group)
            expression = expression.replace("$__rate_interval", "1m").replace("$__range", "15m")
            expression = re.sub(r"\$\{[a-z_]+:regex\}", ".*", expression)
            url = "http://127.0.0.1:19090/api/v1/query?" + urllib.parse.urlencode(
                {"query": expression}
            )
            with urllib.request.urlopen(url, timeout=5) as response:
                result = json.load(response)
            assert result["status"] == "success", (panel["title"], result)
            count += 1
print("PromQL queries succeeded:", count)
print("dashboard panels:", sum(1 for d in dashboards for _ in panels(d["panels"])))
