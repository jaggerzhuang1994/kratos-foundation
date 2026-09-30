#!/usr/bin/env python3
"""验证 ACK 面板查询；显式选择可选组件后，同时验证其指标接入与视图关联。"""
import argparse
import json
import math
import urllib.parse
import urllib.request
from pathlib import Path

from check_dashboards import dashboard_views, expression, panels

COMPONENTS = {
    'server': ('ack-application-components', [('server_requests_code_total', 3)]),
    'client': ('ack-application-components', [('client_requests_code_total', 12)]),
    'sql': ('ack-application-components', [('go_sql_in_use_connections', 26)]),
    'redis': ('ack-application-components', [('db_client_connections_usage', 33)]),
    'cache': ('ack-application-components', [('business_cache_lookups_total', 42)]),
    'queue': ('ack-application-components', [('queue_producer_messages_total', 51), ('queue_consumer_attempts_total', 55)]),
    'kafka': ('ack-application-components', [('kafka_producer_messages_total', 60), ('kafka_consumer_attempts_total', 64)]),
    'job': ('ack-application-components', [('job_running', 69)]),
    'lock': ('ack-application-components', [('lock_operations_total', 74)]),
    'oss': ('ack-application-components', [('oss_requests_total', 79)]),
    'go': ('ack-application-components', [('go_goroutines', 85)]),
    'queue-stats': ('foundation-shared-resources', [('queue_tasks', None)]),
    'kafka-lag': ('foundation-shared-resources', [('kafka_consumergroup_lag', None)]),
    'probe': ('foundation-shared-resources', [('probe_success', None)]),
}


def finite_samples(samples):
    # histogram 没有观测时可能返回 NaN；它不能证明指标已成功展示。
    return any(math.isfinite(float(sample['value'][1])) for sample in samples)


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
    parser.add_argument('--components', default='', help='明确启用的组件，逗号分隔：' + ','.join(COMPONENTS))
    parser.add_argument('--app-pod-label', choices=['pod_name', 'pod'], default='pod_name', help='与面板的应用 Pod 标签来源一致')
    args = parser.parse_args()
    enabled = {name.strip() for name in args.components.split(',') if name.strip()}
    unknown = enabled - COMPONENTS.keys()
    if unknown:
        parser.error('未知组件: ' + ','.join(sorted(unknown)))

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
    dashboards = {d['uid']: d for d in [json.loads(path.read_text()) for path in
                  sorted((Path(__file__).parent / 'grafana/dashboards').glob('*.json'))]}
    required_queries = set()
    for component in sorted(enabled):
        uid, representatives = COMPONENTS[component]
        dashboard = dashboards[uid]
        found = False
        for metric, panel_id in representatives:
            if not finite_samples(query('count(' + metric + ')')):
                continue
            candidates = [p for p in panels(dashboard) if p.get('targets') and (panel_id is None or p['id'] == panel_id)]
            matched = next(((p, i) for p in candidates for i, t in enumerate(p['targets']) if metric + '{' in t['expr']), None)
            if matched is None:
                raise RuntimeError(f'组件 {component} 未找到对应面板查询')
            panel, target = matched
            required_queries.add((uid, panel['id'], target))
            found = True
        if not found:
            metrics = ','.join(metric for metric, _ in representatives)
            raise RuntimeError(f'已启用组件 {component} 缺少代表指标 {metrics}；请检查埋点或 exporter')
    count = 0
    for dashboard in dashboards.values():
        if args.grafana_url:
            loaded = read_json(args.grafana_url.rstrip('/') + '/api/dashboards/uid/' + dashboard['uid'])['dashboard']
            if loaded['panels'] != dashboard['panels'] or loaded['templating'] != dashboard['templating']:
                raise RuntimeError('Grafana 尚未加载当前版本: ' + dashboard['title'])
        for panel in panels(dashboard):
            for target_index, target in enumerate(panel.get('targets', [])):
                for view in dashboard_views(dashboard):
                    samples = query(expression(target['expr'], view, app_pod_label=args.app_pod_label))
                    infrastructure = dashboard['uid'] == 'ack-container-overview' and panel['id'] in {9, 10, 18, 19}
                    required = infrastructure or (dashboard['uid'], panel['id'], target_index) in required_queries
                    if required and not finite_samples(samples):
                        raise RuntimeError(panel['title'] + f' 在 {view or "共享资源"} 无有效数据；检查标签、采集间隔和关联')
                    count += 1
        print('查询通过:', dashboard['title'])
    print('PromQL 查询通过:', count, '；不代表浏览器变量插值已验证')
    print('已验证组件代表指标及关联:', ','.join(sorted(enabled)) or '无（未指定 --components）')
    print('未验证可选组件接入:', ','.join(sorted(COMPONENTS.keys() - enabled)) or '无')


if __name__ == '__main__':
    main()
