#!/usr/bin/env python3
"""把同一份 Dashboard 和告警规则包装成 Kubernetes 资源，不连接集群。"""
import argparse
import json
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--namespace", required=True, help="监控组件所在 namespace")
parser.add_argument("--release", required=True, help="匹配 Prometheus ruleSelector 的 release label")
parser.add_argument("--output", type=Path, required=True)
args = parser.parse_args()
root = Path(__file__).resolve().parent
args.output.mkdir(parents=True, exist_ok=True)
dashboards = {}
for path in sorted((root / "grafana/dashboards").glob("*.json")):
    text = path.read_text()
    json.loads(text)
    dashboards[path.name] = text
# 每个 Grafana Folder 一个 ConfigMap；sidecar 配置 folderAnnotation=grafana_folder 时按注解建目录，否则全部进默认 Folder。
folders = json.loads((root / "grafana/folders.json").read_text())
listed = [name for folder in folders for name in folder["dashboards"]]
if sorted(listed) != sorted(dashboards):
    raise SystemExit(f"grafana/folders.json 与面板文件不一致: {sorted(set(listed) ^ set(dashboards))}")
config_maps = {
    "apiVersion": "v1", "kind": "List",
    "items": [{
        "apiVersion": "v1", "kind": "ConfigMap",
        "metadata": {"name": folder["configMap"], "namespace": args.namespace,
                     "labels": {"grafana_dashboard": "1"}, "annotations": {"grafana_folder": folder["folder"]}},
        "data": {name: dashboards[name] for name in folder["dashboards"]},
    } for folder in folders],
}
(args.output / "dashboard.json").write_text(json.dumps(config_maps, ensure_ascii=False, indent=2) + "\n")
# JSON 字符串也是合法 YAML scalar，避免命令参数被解释成 YAML 语法。
header = ('apiVersion: monitoring.coreos.com/v1\nkind: PrometheusRule\nmetadata:\n'
          '  name: foundation-app\n  namespace: ' + json.dumps(args.namespace) + '\n'
          '  labels:\n    release: ' + json.dumps(args.release) + '\nspec:\n')
rules = (root / "prometheus/alerts.yaml").read_text()
(args.output / "rules.yaml").write_text(header + "".join("  " + line + "\n" for line in rules.splitlines()))
print(f"已生成 {args.output / 'dashboard.json'}（{len(folders)} 个 Folder、{len(dashboards)} 份面板）和 {args.output / 'rules.yaml'}；尚未部署")
