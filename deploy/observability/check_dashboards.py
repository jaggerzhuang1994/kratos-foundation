#!/usr/bin/env python3
"""静态检查面板，并用合成 ACK 样本执行真实 PromQL；不连接业务 Prometheus。"""
import argparse
import json
import re
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parent
VIEWS = ('view_pod', 'view_container', 'view_node', 'view_instance')


def panels(dashboard):
    for panel in dashboard['panels']:
        yield panel
        yield from panels({'panels': panel.get('panels', [])})


def expression(query, view='view_pod', **filters):
    query = query.replace('${view:raw}', view).replace('${app_pod_label:raw}', filters.pop('app_pod_label', 'pod_name'))
    for name, value in filters.items():
        query = query.replace('${' + name + '}', value)
    return re.sub(r'\$\{\w+\}', '.*', query).replace('$__rate_interval', '5m').replace('$__range', '10m')


def fixture_tests(overview):
    by_title = {p['title']: p for p in panels(overview) if p.get('targets')}
    series = []

    def add(metric, labels, values):
        text = ','.join(k + '=' + json.dumps(v) for k, v in labels.items())
        series.append({'series': metric + '{' + text + '}', 'values': values})

    for ns, node, ip, cpu, memory in [('one', 'node-a', '10.0.0.1', 60, 100), ('two', 'node-b', '10.0.0.2', 240, 400)]:
        identity = {'namespace': ns, 'pod': 'api-0'}
        # 两个 kube-state-metrics 抓取副本，不应把同一对象计数两次。
        for replica in ['ksm-a', 'ksm-b']:
            add('kube_pod_info', dict(identity, node=node, pod_ip=ip, instance=replica), '1+0x10')
        add('kube_pod_status_phase', dict(identity, phase='Running'), '1+0x10')
        add('kube_pod_status_ready', dict(identity, condition='true'), ('1' if ns == 'one' else '0') + '+0x10')
        for replica in ['kubelet-a', 'kubelet-b']:
            add('container_cpu_usage_seconds_total', dict(identity, container='api', instance=replica), f'0+{cpu}x10')
            add('container_memory_working_set_bytes', dict(identity, container='api', instance=replica), f'{memory}+0x10')
        add('kube_pod_container_status_running', dict(identity, container='api'), '1+0x10')
        add('kube_pod_container_resource_limits', dict(identity, container='api', resource='cpu', unit='core'), f'{cpu / 30}+0x10')
        add('kube_pod_container_resource_limits', dict(identity, container='api', resource='memory', unit='byte'), f'{memory * 2}+0x10')
        app = {'namespace': ns, 'pod_name': 'api-0', 'container': 'api', 'operation': '/base_service.BaseService/GetAllCurrencyList', 'kind': 'grpc'}
        add('server_requests_code_total', dict(app, code='200'), '0+54x10' if ns == 'one' else '0+120x10')
        for le in ['1', '+Inf']:
            add('server_requests_seconds_bucket', dict(app, le=le), '0+60x10')
        if ns == 'one':
            add('server_requests_code_total', dict(app, code='500'), '0+6x10')
        add('kube_node_info', {'node': node}, '1+0x10')
        add('kube_node_status_condition', {'node': node, 'condition': 'Ready', 'status': 'true'}, '1+0x10')
        add('node_uname_info', {'instance': node + ':9100', 'nodename': node} if ns == 'one' else {'instance': node + ':9100', 'node': node, 'nodename': 'different-hostname'}, '1+0x10')
        add('node_cpu_seconds_total', {'instance': node + ':9100', 'cpu': '0', 'mode': 'idle'}, '0+30x10')
        add('node_memory_MemAvailable_bytes', {'instance': node + ':9100'}, '400+0x10')
        add('node_memory_MemTotal_bytes', {'instance': node + ':9100'}, '1000+0x10')
    # 同 Pod 的 sidecar 无 Limit：使用量应计入总量，但不能污染已设 Limit 的比例。
    extra = {'namespace': 'one', 'pod': 'api-0', 'container': 'sidecar'}
    add('container_cpu_usage_seconds_total', extra, '0+120x10')
    add('container_memory_working_set_bytes', extra, '200+0x10')
    add('kube_pod_container_status_running', extra, '0+0x10')
    # pause 容器和空 cgroup 不得计入 CPU/内存。
    for container in ['', 'POD']:
        add('container_cpu_usage_seconds_total', dict(extra, container=container), '0+60000x10')
    checks = []

    def check(title, expected, view='view_pod', **filters):
        samples = [{'labels': labels, 'value': value} for labels, value in expected]
        checks.append({'expr': expression(by_title[title]['targets'][0]['expr'], view, **filters), 'eval_time': '10m', 'exp_samples': samples})

    check('Ready 节点', [('{}', 2)])
    check('Running Pod', [('{}', 2)])
    check('Ready Pod', [('{}', 1)])
    check('运行中容器', [('{}', 2)])
    dimensions = {
        'view_pod': [('one / api-0', 3), ('two / api-0', 4)],
        'view_container': [('one / api-0 / api', 1), ('one / api-0 / sidecar', 2), ('two / api-0 / api', 4)],
        'view_node': [('node-a', 3), ('node-b', 4)],
        'view_instance': [('one / api-0 / 10.0.0.1', 3), ('two / api-0 / 10.0.0.2', 4)],
    }
    for view, values in dimensions.items():
        check('CPU 使用量', [('{'+view+'='+json.dumps(label)+'}', value) for label, value in values], view)
    check('CPU 使用量', [('{view_pod="one / api-0"}', 1)], namespace='one', container='api')
    check('CPU 使用量', [('{view_pod="two / api-0"}', 4)], node='node-b')
    check('内存工作集', [('{view_pod="one / api-0"}', 300), ('{view_pod="two / api-0"}', 400)])
    for title in ['CPU / 容器 Limit', '内存 / 容器 Limit']:
        check(title, [('{view_pod="one / api-0"}', .5), ('{view_pod="two / api-0"}', .5)])
    check('服务端 5xx 比例', [('{view_pod="one / api-0"}', .1), ('{view_pod="two / api-0"}', 0)])
    check('请求速率', [('{view_pod="one / api-0"}', 1), ('{view_pod="two / api-0"}', 2)])
    check('请求 P95', [('{view_pod="one / api-0"}', .95), ('{view_pod="two / api-0"}', .95)])
    check('节点 CPU 使用率', [('{node="node-a"}', .5), ('{node="node-b"}', .5)], namespace='one')
    check('节点内存使用率', [('{node="node-b"}', .6)], node='node-b')
    # 同一服务若改用标准 pod 标签，隐藏标签选项也须有实际样本验证。
    alternate = [dict(item, series=item['series'].replace('pod_name=', 'pod=')) for item in series if item['series'].startswith('server_requests_code_total') or item['series'].startswith('kube_pod_info')]
    alternate_check = {'eval_time': '10m'}
    alternate_check['expr'] = expression(by_title['请求速率']['targets'][0]['expr'], app_pod_label='pod')
    alternate_check['exp_samples'] = [{'labels': '{view_pod="one / api-0"}', 'value': 1}, {'labels': '{view_pod="two / api-0"}', 'value': 2}]
    return [{'interval': '1m', 'input_series': series, 'promql_expr_test': checks},
            {'interval': '1m', 'input_series': alternate, 'promql_expr_test': [alternate_check]}]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--container', help='使用现有容器中的 promtool；省略时用固定 Prometheus 镜像启动临时容器')
    args = parser.parse_args()
    dashboards = [json.loads(p.read_text()) for p in sorted((ROOT / 'grafana/dashboards').glob('*.json'))]
    overview = next(d for d in dashboards if d['uid'] == 'ack-container-overview')
    empty_checks = []
    for d in dashboards:
        names = {v['name'] for v in d['templating']['list']}
        assert set(re.findall(r'\$\{(\w+)(?::\w+)?\}', json.dumps(d))) <= names
        assert not re.search(r'\$\{\w+:regex\}', json.dumps(d)), '使用 Prometheus 默认转义'
        ids = [p['id'] for p in panels(d)]
        assert len(ids) == len(set(ids))
        view = next(v for v in d['templating']['list'] if v['name'] == 'view')
        assert {o['value'] for o in view['options']} == set(VIEWS)
        for p in panels(d):
            for t in p.get('targets', []):
                assert 'foundation=' not in t['expr'] and '${instance}' not in t['expr']
                for mode in VIEWS:
                    expected = [{'labels': '{}', 'value': 0}] if 'or vector(0)' in t['expr'] else []
                    empty_checks.append({'expr': expression(t['expr'], mode), 'eval_time': '10m', 'exp_samples': expected})
    tests = {'rule_files': [], 'evaluation_interval': '1m', 'tests': [{'interval': '1m', 'promql_expr_test': empty_checks}] + fixture_tests(overview)}
    cmd = ['docker', 'exec', '-i', args.container, 'promtool'] if args.container else ['docker', 'run', '--rm', '-i', '--entrypoint', 'promtool', 'prom/prometheus:v3.5.5']
    subprocess.run(cmd + ['test', 'rules', '/dev/stdin'], input=json.dumps(tests), text=True, check=True)
    print(f'JSON、变量和 {len(empty_checks)} 条视图查询检查通过；合成样本覆盖隔离、去重、Limit 与节点口径')


if __name__ == '__main__':
    main()
