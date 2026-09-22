#!/usr/bin/env python3
"""在已接入 ACK 指标的 Prometheus 上验证四种视图，可选检查 Grafana 导入。"""
import argparse
import json
import urllib.parse
import urllib.request
from pathlib import Path

from check_dashboards import VIEWS, expression, panels


def read_json(url):
    # 返回体有界，连接与读取失败直接暴露，不把采集失败解释为无异常。
    with urllib.request.urlopen(url, timeout=15) as response:
        data = response.read(8 * 1024 * 1024 + 1)
    if len(data) > 8 * 1024 * 1024:
        raise RuntimeError('监控响应超过 8 MiB，请缩小查询范围')
    return json.loads(data)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--prometheus-url', required=True)
    parser.add_argument('--grafana-url')
    args = parser.parse_args()

    def query(expr):
        url = args.prometheus_url.rstrip('/') + '/api/v1/query?' + urllib.parse.urlencode({'query': expr})
        result = read_json(url)
        if result.get('status') != 'success':
            raise RuntimeError(f'Prometheus 查询失败: {result.get("error", "unknown error")}')
        return result['data']['result']

    # 先验证基础设施接入；普通 Compose 不具备这些指标，不能把空查询当作联调成功。
    for metric in ['kube_pod_info', 'kube_node_info', 'container_cpu_usage_seconds_total',
                   'container_memory_working_set_bytes', 'node_uname_info', 'node_cpu_seconds_total']:
        if not query('count(' + metric + ')'):
            raise RuntimeError(f'缺少 ACK 核心指标 {metric}；请检查对应采集组件')
    count = 0
    for path in sorted((Path(__file__).parent / 'grafana/dashboards').glob('*.json')):
        dashboard = json.loads(path.read_text())
        if args.grafana_url:
            loaded = read_json(args.grafana_url.rstrip('/') + '/api/dashboards/uid/' + dashboard['uid'])['dashboard']
            if loaded['panels'] != dashboard['panels'] or loaded['templating'] != dashboard['templating']:
                raise RuntimeError('Grafana 尚未加载当前版本: ' + dashboard['title'])
        for panel in panels(dashboard):
            for target in panel.get('targets', []):
                for view in VIEWS:
                    samples = query(expression(target['expr'], view))
                    if panel['title'] in {'CPU 使用量', '内存工作集', '节点 CPU 使用率', '节点内存使用率'} and not samples:
                        raise RuntimeError(panel['title'] + ' 无数据；检查标签和节点关联')
                    count += 1
        print('查询通过:', dashboard['title'])
    print('四种视图的 PromQL 查询通过:', count, '；不代表浏览器变量插值已验证')


if __name__ == '__main__':
    main()
