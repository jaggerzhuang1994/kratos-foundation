#!/usr/bin/env python3
"""静态检查面板，并用合成 ACK 样本执行真实 PromQL；不连接业务 Prometheus。"""
import argparse
import json
import re
import subprocess
from pathlib import Path

# 面板与规则源在仓库 observability/，本目录只放校验与本地演示环境。
ROOT = Path(__file__).resolve().parents[2] / 'observability'
VIEWS = ('view_pod', 'view_container', 'view_node', 'view_instance')
# 顶部导航顺序即 Folder 顺序：00 总览 / 10 服务 / 20 中间件 / 30 基础设施。
NAV_ORDER = ('foundation-fleet-overview', 'ack-service-overview', 'foundation-gateway', 'ack-application-components',
             'foundation-database', 'foundation-redis', 'foundation-messaging', 'ack-container-overview', 'foundation-shared-resources')
EXPECTED_UIDS = set(NAV_ORDER)
FOLDERS = ('00 总览', '10 服务', '20 中间件', '30 基础设施')


def panels(dashboard):
    for panel in dashboard['panels']:
        yield panel
        yield from panels({'panels': panel.get('panels', [])})


def expression(query, view='view_pod', **filters):
    query = query.replace('${view:raw}', view or 'view_pod').replace('${app_pod_label:raw}', filters.pop('app_pod_label', 'pod_name'))
    for name, value in filters.items():
        query = query.replace('${' + name + '}', value)
    return re.sub(r'\$\{\w+\}', '.*', query).replace('$__rate_interval', '5m').replace('$__range', '10m')


def dashboard_views(dashboard):
    # 共享资源来自逻辑队列或外部 exporter，不需要套用 Pod/Node 关联。
    view = next((v for v in dashboard['templating']['list'] if v['name'] == 'view'), None)
    if view is None:
        return (None,)
    assert {o['value'] for o in view['options']} == set(VIEWS)
    return VIEWS


def input_series(metric, labels, values):
    text = ','.join(k + '=' + json.dumps(v) for k, v in labels.items())
    return {'series': metric + '{' + text + '}', 'values': values}


def expected_samples(expected):
    # 直接选择 recording rule 或 ALERTS 的查询保留指标名，期望样本用 __name__ 键表达。
    return [{'labels': labels.get('__name__', '') + '{' + ','.join(k + '=' + json.dumps(v) for k, v in labels.items() if k != '__name__') + '}',
             'value': value} for labels, value in expected]


def fixture_check(dashboard, panel_id, expected, view='view_pod', target=0, **filters):
    panel = next(p for p in panels(dashboard) if p['id'] == panel_id)
    if isinstance(target, str):
        target = next(i for i, t in enumerate(panel['targets']) if t['refId'] == target)
    return {'expr': expression(panel['targets'][target]['expr'], view, **filters),
            'eval_time': '10m', 'exp_samples': expected_samples(expected)}


def merged(*dashboards):
    """组件区块迁到中间件详情页后面板 ID 保持不变，跨页按 ID 定位。"""
    ids = [p['id'] for d in dashboards for p in panels(d)]
    assert len(ids) == len(set(ids)), '迁移的组件面板 ID 须跨页唯一'
    return {'panels': [p for d in dashboards for p in d['panels']]}


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
        add('kube_pod_container_info', dict(identity, container='api', image='api:v1'), '1+0x10')
        add('kube_pod_container_resource_limits', dict(identity, container='api', resource='cpu', unit='core'), f'{cpu / 30}+0x10')
        add('kube_pod_container_resource_limits', dict(identity, container='api', resource='memory', unit='byte'), f'{memory * 2}+0x10')
        app = {'namespace': ns, 'app': 'api', 'pod_name': 'api-0', 'container': 'api', 'operation': '/base_service.BaseService/GetAllCurrencyList', 'kind': 'grpc'}
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
    add('kube_pod_container_info', dict(extra, image='sidecar:v1'), '1+0x10')
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
    for panel, total in [(2, 2), (3, 2), (4, 2), (5, 3)]:
        checks.append(fixture_check(overview, panel, [({}, total)], target=1))
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
    check('服务端 5xx 比例', [('{view_pod="one / api-0",namespace="one",app="api"}', .1), ('{view_pod="two / api-0",namespace="two",app="api"}', 0)])
    check('请求速率', [('{view_pod="one / api-0",namespace="one",app="api"}', 1), ('{view_pod="two / api-0",namespace="two",app="api"}', 2)])
    check('请求 P95', [('{view_pod="one / api-0",namespace="one",app="api"}', .95), ('{view_pod="two / api-0",namespace="two",app="api"}', .95)])
    check('节点 CPU 使用率', [('{node="node-a"}', .5), ('{node="node-b"}', .5)], namespace='one')
    check('节点内存使用率', [('{node="node-b"}', .6)], node='node-b')
    # 同一服务若改用标准 pod 标签，可选标签来源也须有实际样本验证。
    alternate = [dict(item, series=item['series'].replace('pod_name=', 'pod=')) for item in series if item['series'].startswith('server_requests_code_total') or item['series'].startswith('kube_pod_info')]
    alternate_check = {'eval_time': '10m'}
    alternate_check['expr'] = expression(by_title['请求速率']['targets'][0]['expr'], app_pod_label='pod')
    alternate_check['exp_samples'] = [{'labels': '{view_pod="one / api-0",namespace="one",app="api"}', 'value': 1}, {'labels': '{view_pod="two / api-0",namespace="two",app="api"}', 'value': 2}]
    return [{'interval': '1m', 'input_series': series, 'promql_expr_test': checks},
            {'interval': '1m', 'input_series': alternate, 'promql_expr_test': [alternate_check]}]


def node_fixture_tests(overview):
    series = [input_series('kube_node_info', {'node': 'node-a'}, '1+0x10')]
    for job in ['node-exporter-a', 'node-exporter-b']:
        labels = {'instance': 'node-a:9100', 'job': job}
        series.append(input_series('node_uname_info', dict(labels, node='node-a'), '1+0x10'))
        for cpu in ['0', '1']:
            series.append(input_series('node_cpu_seconds_total', dict(labels, cpu=cpu, mode='idle'), '0+30x10'))
        series.append(input_series('node_load15', labels, '2+0x10'))
        for mountpoint, available in [('/', 200), ('/data', 700)]:
            filesystem = dict(labels, device='/dev/sda', mountpoint=mountpoint, fstype='ext4')
            series.append(input_series('node_filesystem_avail_bytes', filesystem, f'{available}+0x10'))
            series.append(input_series('node_filesystem_size_bytes', filesystem, '1000+0x10'))
        filesystem = dict(labels, device='tmpfs', mountpoint='/run', fstype='tmpfs')
        series.append(input_series('node_filesystem_avail_bytes', filesystem, '1+0x10'))
        series.append(input_series('node_filesystem_size_bytes', filesystem, '1000+0x10'))
        for metric, device, step in [('node_network_receive_bytes_total', 'eth0', 60000),
                                     ('node_network_transmit_bytes_total', 'eth0', 120000),
                                     ('node_disk_read_bytes_total', 'sda', 30000),
                                     ('node_disk_written_bytes_total', 'sda', 60000)]:
            series.append(input_series(metric, dict(labels, device=device), f'0+{step}x10'))
    checks = [fixture_check(overview, panel, [({'node': 'node-a'}, value)], target=target)
              for panel, target, value in [(20, 0, 1), (22, 0, 1000), (22, 1, 2000), (23, 0, 500), (23, 1, 1000)]]
    for target, values in [(0, [.8, 1 - .7]), (1, [200, 700])]:
        checks.append(fixture_check(overview, 47, [({'node': 'node-a', 'instance': 'node-a:9100', 'device': '/dev/sda',
                                                   'mountpoint': mountpoint, 'fstype': 'ext4'}, value)
                                                  for mountpoint, value in zip(['/', '/data'], values)], target=target))
    return [{'interval': '1m', 'input_series': series, 'promql_expr_test': checks}]


def resource_state_fixture_tests(overview):
    series, phases, unavailable = [], [], []
    for index, phase in enumerate(['Pending', 'Running', 'Succeeded', 'Failed', 'Unknown'], 1):
        pod = phase.lower() + '-0'
        identity = {'namespace': 'one', 'pod': pod}
        labels = dict(identity, node='node-a', pod_ip='10.0.0.' + str(index))
        phases.append((labels, index))
        if phase not in ['Running', 'Succeeded']:
            unavailable.append((labels, 0))
        for replica in ['a', 'b']:
            series.append(input_series('kube_pod_info', dict(labels, instance=replica), '1+0x10'))
            series.append(input_series('kube_pod_status_phase', dict(identity, phase=phase, instance=replica), '1+0x10'))
            series.append(input_series('kube_pod_status_ready', dict(identity, condition='true', instance=replica), ('1' if phase == 'Running' else '0') + '+0x10'))
    oom = {'namespace': 'one', 'pod': 'failed-0', 'container': 'api', 'reason': 'OOMKilled'}
    series.append(input_series('kube_pod_container_status_last_terminated_reason', oom, '1+0x10'))
    series.append(input_series('kube_node_status_condition', {'node': 'node-a', 'condition': 'Ready', 'status': 'false'}, '1+0x10'))
    for namespace, desired, available in [('one', 3, 1), ('two', 1, 2)]:
        for replica in ['a', 'b']:
            labels = {'namespace': namespace, 'deployment': 'api', 'instance': replica}
            series.append(input_series('kube_deployment_spec_replicas', labels, f'{desired}+0x10'))
            series.append(input_series('kube_deployment_status_replicas_available', labels, f'{available}+0x10'))
    checks = [fixture_check(overview, 29, phases), fixture_check(overview, 45, unavailable),
              fixture_check(overview, 44, [({'node': 'node-a', 'status': 'false'}, 1)]),
              fixture_check(overview, 46, [(dict(oom, node='node-a', pod_ip='10.0.0.4'), 1)])]
    for target, values in [(0, [3, 1]), (1, [1, 2]), (2, [2, 0])]:
        checks.append(fixture_check(overview, 48, [({'namespace': namespace, 'deployment': 'api'}, value)
                                                  for namespace, value in zip(['one', 'two'], values)], target=target))
    checks.append(fixture_check(overview, 48, [({'namespace': 'one', 'deployment': 'api'}, 2)], target=2, namespace='one', deployment='api'))
    return [{'interval': '1m', 'input_series': series, 'promql_expr_test': checks}]


def component_fixture_tests(components):
    series, checks = [], []

    def add(metric, labels, values):
        series.append(input_series(metric, labels, values))

    identities = [('one', 'api', 'api-0', '10.0.0.1', 9, 10, 10),
                  ('one', 'api', 'api-1', '10.0.0.2', 1, 90, 20),
                  ('one', 'worker', 'worker-0', '10.0.0.3', 2, 10, 30),
                  ('two', 'api', 'api-0', '10.0.0.4', 4, 10, 40)]
    for namespace, app, pod, ip, used, limit, goroutines in identities:
        labels = {'namespace': namespace, 'app': app, 'pod_name': pod, 'container': 'api', 'instance': ip + ':9000'}
        # 同节点、同 db_name 仍须按应用与命名空间隔离；利用率必须先逐池除再取最大值。
        add('kube_pod_info', {'namespace': namespace, 'pod': pod, 'node': 'node-a', 'pod_ip': ip}, '1+0x10')
        add('go_sql_in_use_connections', dict(labels, db_name='primary'), f'{used}+0x10')
        add('go_sql_max_open_connections', dict(labels, db_name='primary'), f'{limit}+0x10')
        add('database_sql_operations_total', dict(labels, db_name='primary', operation='select', result='success'), '0+60x10')
        add('go_goroutines', labels, f'{goroutines}+0x10')
    main = {'namespace': 'one', 'app': 'api', 'pod_name': 'api-0', 'container': 'api', 'instance': '10.0.0.1:9000'}
    add('database_sql_operations_total', dict(main, db_name='primary', operation='idle', result='success'), '0+0x10')
    add('go_sql_wait_count_total', dict(main, db_name='primary'), '0+60x10')
    add('go_sql_wait_duration_seconds_total', dict(main, db_name='primary'), '0+3x10')
    redis = dict(main, db_system='redis', redis_connection='primary', pool_name='default')
    add('db_client_connections_waits', redis, '0+60x10')
    add('db_client_connections_waits_duration_nanoseconds', redis, '0+120000000x10')
    add('db_client_connections_use_time_milliseconds_count', dict(redis, type='command', status='success'), '0+60x10')
    for le in ['100', '+Inf']:
        add('db_client_connections_use_time_milliseconds_bucket', dict(redis, type='command', status='success', le=le), '0+60x10')
        add('db_client_connections_create_time_milliseconds_bucket', dict(redis, status='success', le=le), '0+60x10')
    for result, step in [('hit', 48), ('miss', 12), ('error', 60)]:
        add('business_cache_lookups_total', dict(main, cache_name='users', result=result), f'0+{step}x10')
    add('job_runs_total', dict(main, job='scrape-target', exported_job='sync-users', status='success'), '0+60x10')
    for result, step in [('success', 45), ('contended', 15)]:
        add('lock_operations_total', dict(main, lock_name='leader', operation='try_lock', result=result), f'0+{step}x10')
    add('oss_requests_total', dict(main, bucket='assets', operation='get', result='success'), '0+60x10')
    for le in ['1', '+Inf']:
        add('lock_operation_duration_seconds_bucket', dict(main, lock_name='leader', operation='lock', le=le), '0+60x10')
        add('queue_consumer_attempt_duration_seconds_bucket', dict(main, queue_destination='emails', queue_consumer='mailer', le=le), '0+60x10')
        add('kafka_consumer_attempt_duration_seconds_bucket', dict(main, kafka_destination='events', kafka_consumer='worker', le=le), '0+60x10')
    add('go_gc_duration_seconds_sum', main, '0+3x10')
    add('go_gc_duration_seconds_count', main, '0+60x10')
    for observer, age, known, success in [('a', 50, 1, 1), ('b', 999, 0, 1), ('c', 2000, 1, 0)]:
        labels = dict(main, queue_destination='emails', prometheus_replica=observer)
        # 先按原始 observer 标签屏蔽无效年龄，再关联 Pod；副本之间不能借用彼此的采集状态。
        for metric, value in [('queue_oldest_ready_age_seconds', age), ('queue_stats_oldest_ready_known', known),
                              ('queue_stats_collection_success', success)]:
            add(metric, labels, f'{value}+0x10')
    isolation = [({'view_node': 'node-a', 'namespace': ns, 'app': app, 'db_name': 'primary'}, value)
                 for ns, app, value in [('one', 'api', .9), ('one', 'worker', .2), ('two', 'api', .4)]]
    checks.append(fixture_check(components, 29, isolation, 'view_node'))
    checks.append(fixture_check(components, 22, [(dict(labels, operation='select'), 0) for labels, _ in isolation], 'view_node'))
    checks.append(fixture_check(components, 48, [({'namespace': 'one', 'app': 'api', 'view_node': 'node-a', 'queue_destination': 'emails'}, 50)], 'view_node'))
    # 没流量不应显示“错误率 0”；零分母保留空结果。
    checks.append(fixture_check(components, 85, [({'view_node': 'node-a', 'namespace': ns, 'app': app}, value)
                                               for ns, app, value in [('one', 'api', 30), ('one', 'worker', 30), ('two', 'api', 40)]], 'view_node'))
    for view in VIEWS:
        identity = {'namespace': 'one', 'app': 'api', view: {'view_pod': 'one / api-0', 'view_container': 'one / api-0 / api',
                                                          'view_node': 'node-a', 'view_instance': 'one / api-0 / 10.0.0.1'}[view]}
        cases = [(31, {'db_name': 'primary'}, .05),
                 (38, {'redis_connection': 'primary', 'pool_name': 'default', 'type': 'command'}, 0),
                 (39, {'redis_connection': 'primary', 'pool_name': 'default', 'type': 'command'}, 95),
                 (40, {'redis_connection': 'primary', 'pool_name': 'default'}, .002),
                 (43, {'cache_name': 'users'}, .8),
                 (70, {'exported_job': 'sync-users', 'status': 'success', 'event': '完成'}, 600),
                 (75, {'lock_name': 'leader', 'operation': 'lock'}, .95),
                 (76, {'lock_name': 'leader'}, .25),
                 (79, {'bucket': 'assets', 'operation': 'get', 'result': 'success'}, 1),
                 (101, {}, .05),
                 (103, {'redis_connection': 'primary', 'pool_name': 'default', 'status': 'success'}, 95),
                 (105, {'queue_destination': 'emails', 'queue_consumer': 'mailer'}, .95),
                 (106, {'kafka_destination': 'events', 'kafka_consumer': 'worker'}, .95)]
        for panel, labels, value in cases:
            checks.append(fixture_check(components, panel, [(dict(identity, **labels), value)], view, namespace='one', app='api'))
    redis_panel = next(p for p in panels(components) if p['id'] == 39)
    assert redis_panel['fieldConfig']['defaults']['unit'] == 'ms', 'Redis 调用 histogram 原始单位是毫秒'
    zero = [input_series('kube_pod_info', {'namespace': 'one', 'pod': 'api-0', 'node': 'node-a', 'pod_ip': '10.0.0.1'}, '1+0x10')]
    for metric, labels in [('database_sql_operations_total', {'db_name': 'primary', 'operation': 'select', 'result': 'success'}),
                           ('db_client_connections_use_time_milliseconds_count', {'db_system': 'redis', 'redis_connection': 'primary', 'pool_name': 'default', 'type': 'command', 'status': 'success'}),
                           ('business_cache_lookups_total', {'cache_name': 'users', 'result': 'miss'}),
                           ('lock_operations_total', {'lock_name': 'leader', 'operation': 'try_lock', 'result': 'success'}),
                           ('go_gc_duration_seconds_sum', {}), ('go_gc_duration_seconds_count', {})]:
        zero.append(input_series(metric, dict(main, **labels), '0+0x10'))
    unknown_labels = dict(main, queue_destination='emails', prometheus_replica='unknown')
    for metric, value in [('queue_oldest_ready_age_seconds', 999), ('queue_stats_oldest_ready_known', 0), ('queue_stats_collection_success', 1)]:
        zero.append(input_series(metric, unknown_labels, f'{value}+0x10'))
    return [{'interval': '1m', 'input_series': series, 'promql_expr_test': checks},
            {'interval': '1m', 'input_series': zero, 'promql_expr_test': [fixture_check(components, panel, []) for panel in [22, 38, 43, 76, 101]] + [fixture_check(components, 48, [], 'view_node')]}]


def service_fixture_tests(service):
    series, checks = [], []
    for app, pod, ip, ready in [('api', 'api-0', '10.0.0.1', 1), ('worker', 'worker-0', '10.0.0.2', 0)]:
        main = {'namespace': 'one', 'app': app, 'pod_name': pod, 'container': 'api', 'instance': ip + ':9000', 'job': 'application'}
        for replica in ['prometheus-a', 'prometheus-b']:
            series.append(input_series('kube_pod_info', {'namespace': 'one', 'pod': pod, 'node': 'node-a', 'pod_ip': ip, 'instance': replica}, '1+0x10'))
            up = 1 if app == 'api' or replica == 'prometheus-b' else 0
            series.append(input_series('up', dict(main, prometheus_replica=replica), f'{up}+0x10'))
        series.append(input_series('kube_pod_status_ready', {'namespace': 'one', 'pod': pod, 'condition': 'true'}, f'{ready}+0x10'))
        request = dict(main, operation='/users', kind='http')
        series.append(input_series('server_requests_code_total', dict(request, code='200'), '0+60x10' if app == 'api' else '0+30x10'))
        if app == 'worker':
            series.append(input_series('server_requests_code_total', dict(request, code='500'), '0+30x10'))
        for le in ['1', '+Inf']:
            series.append(input_series('server_requests_seconds_bucket', dict(request, le=le), '0+60x10'))
        # app_ready 由两个 Prometheus 副本重复抓取，面板须按 Pod 去重后再求就绪占比。
        for replica in ['prometheus-a', 'prometheus-b']:
            series.append(input_series('app_ready', dict(main, prometheus_replica=replica), f'{ready}+0x10'))
    orphan = {'namespace': 'one', 'app': 'api', 'pod_name': 'orphan-0', 'container': 'api', 'instance': '10.0.0.3:9000', 'job': 'application'}
    series.append(input_series('up', orphan, '1+0x10'))
    for panel, value in [(2, 2), (3, .25), (4, .95), (5, .5), (6, 2), (7, 1)]:
        checks.append(fixture_check(service, panel, [({}, value)]))
    for view in VIEWS:
        def identity(app, pod, ip):
            label = {'view_pod': 'one / ' + pod, 'view_container': 'one / ' + pod + ' / api',
                     'view_node': 'node-a', 'view_instance': 'one / ' + pod + ' / ' + ip}[view]
            return {'namespace': 'one', 'app': app, view: label}
        expected = [identity('api', 'api-0', '10.0.0.1'), identity('worker', 'worker-0', '10.0.0.2')]
        for panel, values in [(9, [1, 1]), (10, [0, .5]), (11, [.95, .95])]:
            checks.append(fixture_check(service, panel, list(zip(expected, values)), view))
    checks.append(fixture_check(service, 19, [({}, .5)]))
    checks.append(fixture_check(service, 19, [({}, 1)], app='api'))
    checks.append(fixture_check(service, 3, [({}, 0)], app='api'))
    checks.append(fixture_check(service, 13, [({'namespace': 'one', 'app': 'worker', 'operation': '/users', 'kind': 'http'}, .5)]))
    checks.append(fixture_check(service, 17, [({'namespace': 'one', 'app': 'api', 'pod': 'orphan-0', 'container': 'api',
                                             'instance': '10.0.0.3:9000', 'job': 'application'}, 1)]))
    zero = [item for item in series if item['series'].startswith('kube_pod_info')]
    zero.append(input_series('server_requests_code_total', {'namespace': 'one', 'app': 'api', 'pod_name': 'api-0', 'container': 'api',
                                                           'operation': '/users', 'kind': 'http', 'code': '200'}, '0+0x10'))
    return [{'interval': '1m', 'input_series': series, 'promql_expr_test': checks},
            {'interval': '1m', 'input_series': zero, 'promql_expr_test': [fixture_check(service, 3, []), fixture_check(service, 10, [])]}]


def shared_fixture_tests(messaging, shared):
    """Queue 库存与 Kafka Lag 已迁到队列与 Kafka 详情（面板 ID +400），健康探测仍在原共享资源 UID。"""
    series = []
    queue = {'env': 'qa', 'cluster': 'ack', 'queue_destination': 'emails'}
    for observer, count, age, known, success in [('a', 12, 40, 1, 1), ('b', 12, 50, 1, 1), ('c', 12, 999, 0, 0)]:
        labels = dict(queue, namespace='one', app='worker', instance=observer)
        series.append(input_series('queue_tasks', dict(labels, state='ready'), f'{count}+0x10'))
        # 失败或未知 observer 的旧年龄样本须被屏蔽，不能参与最老年龄聚合。
        series.append(input_series('queue_oldest_ready_age_seconds', labels, f'{age}+0x10'))
        series.append(input_series('queue_stats_oldest_ready_known', labels, f'{known}+0x10'))
        series.append(input_series('queue_stats_collection_success', labels, f'{success}+0x10'))
    kafka = {'env': 'qa', 'cluster': 'ack', 'kafka_cluster': 'primary', 'consumergroup': 'worker', 'topic': 'events'}
    for exporter in ['a', 'b']:
        for partition, lag in [('0', 7), ('1', 11), ('2', -1)]:
            series.append(input_series('kafka_consumergroup_lag', dict(kafka, partition=partition, instance=exporter), f'{lag}+0x10'))
    series.append(input_series('kafka_consumergroup_lag', dict(kafka, topic='other', partition='0', instance='a'), '1000+0x10'))
    probe = {'env': 'qa', 'cluster': 'ack', 'app': 'api', 'instance': 'https://api/readyz', 'probe': 'readyz'}
    for exporter in ['a', 'b']:
        labels = dict(probe, foundation_probe='true', exporter=exporter)
        series.append(input_series('probe_success', labels, '0+0x10'))
        series.append(input_series('up', labels, '1+0x10'))
        series.append(input_series('probe_http_status_code', labels, '503+0x10'))
        series.append(input_series('probe_duration_seconds', labels, '.25+0x10'))
    # 其他环境同名队列不可并入 qa；过滤器须真的约束共享资源。
    series.append(input_series('queue_tasks', dict(queue, env='prod', state='ready', instance='other'), '1000+0x10'))
    cases = [(messaging, 402, [(dict(queue, state='ready'), 12)]), (messaging, 403, [(queue, 50)]), (messaging, 404, [(queue, 0)]),
             (messaging, 405, [(queue, 0)]), (messaging, 408, [(kafka, 18)]), (messaging, 410, [(dict(kafka, partition='2'), -1)]),
             (messaging, 421, [({}, 18)]), (messaging, 422, [({}, 50)]),
             (shared, 13, [(probe, 0)]), (shared, 14, [(probe, 1)]), (shared, 15, [(probe, 503)]), (shared, 16, [(probe, .25)])]
    checks = [fixture_check(page, panel, expected, env='qa', broker_topic='events') for page, panel, expected in cases]
    unknown_labels = dict(queue, namespace='one', app='worker', instance='unknown')
    unknown = [input_series(metric, unknown_labels, f'{value}+0x10') for metric, value in
               [('queue_oldest_ready_age_seconds', 999), ('queue_stats_collection_success', 1), ('queue_stats_oldest_ready_known', 0)]]
    return [{'interval': '1m', 'input_series': series, 'promql_expr_test': checks},
            {'interval': '1m', 'input_series': unknown, 'promql_expr_test': [fixture_check(messaging, 403, []), fixture_check(messaging, 422, [])]}]


def fleet_fixture_tests(fleet):
    """舰队页只读 recording rules，样本从原始指标经 alerts.yaml 的 foundation-records 预聚合得到。"""
    series = []

    def add(metric, labels, values):
        series.append(input_series(metric, dict({'namespace': 'apps'}, **labels), values))

    add('app_ready', {'app': 'api', 'pod': 'api-0'}, '1+0x10')
    add('app_ready', {'app': 'api', 'pod': 'api-1'}, '0+0x10')
    add('app_ready', {'app': 'worker', 'pod': 'worker-0'}, '1+0x10')
    add('app_ready', {'app': 'cron', 'pod': 'cron-0'}, '0+0x10')
    add('server_requests_code_total', {'app': 'api', 'pod': 'api-0', 'code': '200'}, '0+54x10')
    add('server_requests_code_total', {'app': 'api', 'pod': 'api-1', 'code': '500'}, '0+6x10')
    for le in ['0.5', '+Inf']:
        add('server_requests_seconds_bucket', {'app': 'api', 'pod': 'api-0', 'le': le}, '0+60x10')
    queue = {'app': 'worker', 'pod': 'worker-0', 'queue_destination': 'emails', 'queue_consumer': 'mailer'}
    add('queue_consumer_messages_total', dict(queue, queue_result='success'), '0+48x10')
    add('queue_consumer_messages_total', dict(queue, queue_result='failed'), '0+12x10')
    kafka = {'app': 'worker', 'pod': 'worker-0', 'kafka_destination': 'events', 'kafka_consumer': 'sync'}
    add('kafka_consumer_messages_total', dict(kafka, kafka_result='success'), '0+42x10')
    add('kafka_consumer_messages_total', dict(kafka, kafka_result='error'), '0+18x10')
    add('job_runs_total', {'app': 'cron', 'pod': 'cron-0', 'exported_job': 'cleanup', 'status': 'failure'}, '0+1x10')
    # 采集失败或年龄未知的观察者不得把旧值带进舰队页。
    for instance, age, known in [('a', 30, 1), ('b', 999, 0)]:
        stats = {'app': 'worker', 'pod': 'worker-0', 'instance': instance, 'queue_destination': 'emails'}
        add('queue_oldest_ready_age_seconds', stats, f'{age}+0x10')
        add('queue_stats_oldest_ready_known', stats, f'{known}+0x10')
        add('queue_stats_collection_success', stats, '1+0x10')

    def app(name):
        return {'namespace': 'apps', 'app': name}
    target = {p['refId']: index for index, p in enumerate(next(p for p in panels(fleet) if p['id'] == 9)['targets'])}
    checks = [fixture_check(fleet, panel, [({}, value)]) for panel, value in [(2, 3), (3, 1), (4, 2), (5, 1), (6, .1), (7, 10)]]
    checks.append(fixture_check(fleet, 3, [({}, 0)], namespace='other'))
    for ref, expected in [('A', [(app('api'), 1), (app('worker'), 1), (app('cron'), 0)]),
                          ('B', [(app('api'), 2), (app('worker'), 1), (app('cron'), 1)]),
                          ('C', [(app('api'), 1)]), ('D', [(app('api'), .1)]), ('E', [(app('api'), .475)]),
                          ('F', [(app('worker'), 2)]), ('G', [(app('worker'), .25)]), ('H', [(app('worker'), 30)]),
                          ('I', [(app('cron'), 10)])]:
        checks.append(fixture_check(fleet, 9, expected, target=target[ref]))
    # 无错误样本时比例为 0；无流量时比例不出值，不能把空闲服务显示成 0% 错误。
    idle = [input_series('server_requests_code_total', {'namespace': 'apps', 'app': 'api', 'pod': 'api-0', 'code': '200'}, '0+60x10'),
            input_series('server_requests_code_total', {'namespace': 'apps', 'app': 'idle', 'pod': 'idle-0', 'code': '200'}, '0+0x10')]
    return [{'interval': '1m', 'input_series': series, 'promql_expr_test': checks},
            {'interval': '1m', 'input_series': idle, 'promql_expr_test': [
                fixture_check(fleet, 6, [({}, 0)]), fixture_check(fleet, 9, [(app('api'), 0)], target=target['D'])]},
            {'interval': '1m', 'input_series': idle[1:], 'promql_expr_test': [fixture_check(fleet, 6, []), fixture_check(fleet, 9, [], target=target['D'])]}]


def platform_fixture_tests(fleet):
    """平台总览新增分区：原始 K8s / exporter / 应用样本经 foundation-records 预聚合后再由面板查询。"""
    series = []

    def add(metric, labels, values):
        series.append(input_series(metric, labels, values))

    # 两个 kube-state-metrics 副本；Succeeded 是终态，不算未就绪。
    for namespace, pod, phase, ready in [('apps', 'api-0', 'Running', 1), ('apps', 'api-1', 'Running', 0),
                                         ('apps', 'job-0', 'Succeeded', 0), ('other', 'x-0', 'Pending', 0)]:
        for replica in ['ksm-a', 'ksm-b']:
            identity = {'namespace': namespace, 'pod': pod, 'instance': replica}
            add('kube_pod_info', dict(identity, node='node-a'), '1+0x10')
            add('kube_pod_status_phase', dict(identity, phase=phase), '1+0x10')
            add('kube_pod_status_ready', dict(identity, condition='true'), f'{ready}+0x10')
            add('kube_pod_container_status_restarts_total', dict(identity, container='api'), '0+1x10' if pod == 'api-1' else '0+0x10')
    for node, ready in [('node-a', 'true'), ('node-b', 'false')]:
        add('kube_node_info', {'node': node}, '1+0x10')
        add('kube_node_status_condition', {'node': node, 'condition': 'Ready', 'status': ready}, '1+0x10')
    add('kube_node_status_condition', {'node': 'node-a', 'condition': 'MemoryPressure', 'status': 'true'}, '1+0x10')
    add('kube_node_status_condition', {'node': 'node-b', 'condition': 'MemoryPressure', 'status': 'true'}, '0+0x10')
    # mysqld_exporter：db-a 被两个 Prometheus 副本抓取，db-b 是连不上的副本库。
    for replica in ['a', 'b']:
        db = {'instance': 'db-a', 'prometheus_replica': replica}
        add('mysql_up', db, '1+0x10')
        add('mysql_global_status_threads_connected', db, '80+0x10')
        add('mysql_global_variables_max_connections', db, '100+0x10')
        add('mysql_global_status_slow_queries', db, '0+6x10')
        add('mysql_global_status_connection_errors_total', dict(db, error='internal'), '0+3x10')
        add('mysql_global_status_aborted_connects', db, '0+3x10')
    add('mysql_up', {'instance': 'db-b'}, '0+0x10')
    add('mysql_slave_status_seconds_behind_source', {'instance': 'db-b'}, '12+0x10')
    for pod, used in [('api-0', 9), ('api-1', 1)]:
        pool = {'namespace': 'apps', 'app': 'api', 'pod': pod, 'db_name': 'primary'}
        add('go_sql_in_use_connections', pool, f'{used}+0x10')
        add('go_sql_max_open_connections', pool, '10+0x10')
    redis = {'instance': 'redis-a'}
    add('redis_up', redis, '1+0x10')
    add('redis_memory_used_bytes', redis, '900+0x10')
    add('redis_memory_max_bytes', redis, '1000+0x10')
    add('redis_evicted_keys_total', redis, '0+60x10')
    add('redis_commands_duration_seconds_total', dict(redis, cmd='get'), '0+3x10')
    add('redis_commands_total', dict(redis, cmd='get'), '0+60x10')
    add('redis_connected_clients', redis, '42+0x10')
    kafka = {'kafka_cluster': 'primary', 'consumergroup': 'worker', 'topic': 'events'}
    for partition, lag in [('0', 7), ('1', 11), ('2', -1)]:
        add('kafka_consumergroup_lag', dict(kafka, partition=partition), f'{lag}+0x10')
    for instance, age, known in [('a', 30, 1), ('b', 999, 0)]:
        stats = {'namespace': 'apps', 'app': 'worker', 'pod': 'worker-0', 'instance': instance, 'queue_destination': 'emails'}
        add('queue_oldest_ready_age_seconds', stats, f'{age}+0x10')
        add('queue_stats_oldest_ready_known', stats, f'{known}+0x10')
        add('queue_stats_collection_success', stats, '1+0x10')
    consumer = {'namespace': 'apps', 'app': 'worker', 'pod': 'worker-0', 'kafka_destination': 'events', 'kafka_consumer': 'sync'}
    add('kafka_consumer_messages_total', dict(consumer, kafka_result='success'), '0+42x10')
    add('kafka_consumer_messages_total', dict(consumer, kafka_result='error'), '0+18x10')
    add('kafka_consumer_dead_letters_total', dict(consumer, kafka_result='success'), '0+6x10')
    gateway = {'namespace': 'apps', 'app': 'gw', 'pod': 'gw-0'}
    add('server_requests_code_total', dict(gateway, operation='/api/orders', code='200'), '0+54x10')
    add('server_requests_code_total', dict(gateway, operation='/api/orders', code='500'), '0+6x10')
    for le in ['0.5', '+Inf']:
        add('server_requests_seconds_bucket', dict(gateway, operation='/api/orders', le=le), '0+60x10')
    # gRPC 风格 operation 推导出服务名；没有第二段路径的 operation 原样作为下游。
    add('client_requests_code_total', dict(gateway, operation='/orders.v1.Orders/Get', code='200'), '0+30x10')
    add('client_requests_code_total', dict(gateway, operation='/orders.v1.Orders/Get', code='503'), '0+30x10')
    add('client_requests_code_total', dict(gateway, operation='/healthz', code='200'), '0+60x10')
    for le in ['1', '+Inf']:
        add('client_requests_seconds_bucket', dict(gateway, operation='/orders.v1.Orders/Get', le=le), '0+60x10')

    def rule(name, **labels):
        return dict(labels, __name__=name)
    checks = [fixture_check(fleet, panel, [({}, value)]) for panel, value in
              [(21, 4), (22, 2), (23, 10), (24, 2), (25, 1), (26, 1), (31, 2), (32, 1), (33, .8), (34, 12), (35, .9),
               (41, 1), (42, 0), (43, .9), (44, 1), (45, .05), (51, 18), (52, 30), (53, .1), (54, .3)]]
    checks.append(fixture_check(fleet, 22, [({}, 1)], namespace='apps'))
    checks.append(fixture_check(fleet, 27, [(rule('pod:kube_pod_not_ready:info', namespace='apps', pod='api-1', phase='Running'), 1),
                                            (rule('pod:kube_pod_not_ready:info', namespace='other', pod='x-0', phase='Pending'), 1)]))
    checks.append(fixture_check(fleet, 28, [(rule('pod:kube_pod_container_restarts:increase1h', namespace='apps', pod='api-1'), 10)]))
    checks.append(fixture_check(fleet, 29, [(rule('node:kube_node_not_ready:info', node='node-b', condition='NotReady'), 1),
                                            (rule('node:kube_node_pressure:info', node='node-a', condition='MemoryPressure'), 1)]))
    for ref, expected in [('A', [(rule('instance:mysql_up:max', instance='db-a'), 1), (rule('instance:mysql_up:max', instance='db-b'), 0)]),
                          ('B', [(rule('instance:mysql_connections:usage', instance='db-a'), .8)]),
                          ('C', [(rule('instance:mysql_slow_queries:rate5m', instance='db-a'), .1)]),
                          ('D', [(rule('instance:mysql_connection_errors:rate5m', instance='db-a'), .1)]),
                          ('E', [(rule('instance:mysql_replication_lag:seconds', instance='db-b'), 12)])]:
        checks.append(fixture_check(fleet, 36, expected, target=ref))
    checks.append(fixture_check(fleet, 37, [(rule('app:go_sql_pool_usage:max', namespace='apps', app='api', db_name='primary'), .9)]))
    for ref, expected in [('B', [(rule('instance:redis_memory:usage', instance='redis-a'), .9)]),
                          ('D', [(rule('instance:redis_command_latency:avg5m', instance='redis-a'), .05)]),
                          ('E', [(rule('instance:redis_connected_clients:max', instance='redis-a'), 42)])]:
        checks.append(fixture_check(fleet, 46, expected, target=ref))
    checks.append(fixture_check(fleet, 55, [(rule('group:kafka_consumergroup_lag:sum', **kafka), 18)]))
    checks.append(fixture_check(fleet, 56, [(rule('queue:queue_oldest_ready_age:max', queue_destination='emails'), 30)]))
    checks.append(fixture_check(fleet, 57, [(rule('app:dead_letters:rate5m', namespace='apps', app='worker', consumer_type='kafka'), .1)]))
    gw, orders, health = {'namespace': 'apps', 'app': 'gw'}, 'orders.v1.Orders', '/healthz'
    for ref, expected in [('A', [(gw, 1)]), ('B', [(gw, .1)]), ('C', [(gw, .495)])]:
        checks.append(fixture_check(fleet, 61, expected, target=ref))
    for ref, expected in [('A', [(dict(gw, downstream=orders), 1), (dict(gw, downstream=health), 1)]),
                          ('B', [(dict(gw, downstream=orders), .5), (dict(gw, downstream=health), 0)]),
                          ('C', [(dict(gw, downstream=orders), .99)])]:
        checks.append(fixture_check(fleet, 62, expected, target=ref))
    # exporter 未接入：数据库 / Redis / Kafka 区为空（显示“未接入”），K8s 与消费者已接入时异常计数补 0。
    unconnected = [input_series('kube_pod_info', {'namespace': 'apps', 'pod': 'api-0'}, '1+0x10'),
                   input_series('kube_pod_status_phase', {'namespace': 'apps', 'pod': 'api-0', 'phase': 'Running'}, '1+0x10'),
                   input_series('kube_pod_status_ready', {'namespace': 'apps', 'pod': 'api-0', 'condition': 'true'}, '1+0x10'),
                   input_series('kafka_consumer_messages_total', dict(consumer, kafka_result='success'), '0+60x10')]
    empty = [fixture_check(fleet, panel, []) for panel in [31, 32, 33, 34, 41, 42, 43, 44, 51, 52]]
    empty += [fixture_check(fleet, 36, [], target='A'), fixture_check(fleet, 46, [], target='A'), fixture_check(fleet, 55, [])]
    zero = [fixture_check(fleet, panel, [({}, 0)]) for panel in [22, 23, 53, 54]]
    # ALERTS 由 Prometheus 写入；单独一组输入，避免与其他组的告警评估结果混在一起。
    firing = {'alertname': 'FoundationAppNotReady', 'severity': 'warning', 'namespace': 'apps', 'app': 'api', 'pod': 'api-1'}
    alerts = [input_series('ALERTS', dict(firing, alertstate='firing'), '1+0x10'),
              input_series('ALERTS', dict(firing, alertname='FoundationHighLatency', alertstate='pending'), '1+0x10')]
    return [{'interval': '1m', 'input_series': series, 'promql_expr_test': checks},
            {'interval': '1m', 'input_series': unconnected, 'promql_expr_test': empty + zero},
            {'interval': '1m', 'input_series': alerts, 'promql_expr_test': [
                fixture_check(fleet, 12, [(dict(firing, alertstate='firing', __name__='ALERTS'), 1)]),
                fixture_check(fleet, 12, [], namespace='other')]}]


def service_section_fixture_tests(service):
    """服务详情的消费者 / Job / Runtime 分区与首行负载识别。"""
    series = []
    worker = {'namespace': 'one', 'app': 'worker', 'pod_name': 'worker-0', 'container': 'api'}
    api = {'namespace': 'one', 'app': 'api', 'pod_name': 'api-0', 'container': 'api'}
    for labels, values in [(dict(worker, queue_destination='emails', queue_consumer='mailer', queue_result='success'), '0+45x10'),
                           (dict(worker, queue_destination='emails', queue_consumer='mailer', queue_result='failed'), '0+15x10')]:
        series.append(input_series('queue_consumer_messages_total', labels, values))
    series.append(input_series('kafka_consumer_messages_total', dict(worker, kafka_destination='events', kafka_consumer='sync', kafka_result='success'), '0+60x10'))
    for instance, age, known in [('a', 40, 1), ('b', 999, 0)]:
        stats = dict(worker, instance=instance, queue_destination='emails')
        series.append(input_series('queue_oldest_ready_age_seconds', stats, f'{age}+0x10'))
        series.append(input_series('queue_stats_oldest_ready_known', stats, f'{known}+0x10'))
        series.append(input_series('queue_stats_collection_success', stats, '1+0x10'))
    series.append(input_series('job_running', dict(worker, exported_job='sync'), '1+0x10'))
    series.append(input_series('server_requests_code_total', dict(api, operation='/users', kind='http', code='200'), '0+60x10'))
    for labels, open_fds in [(worker, 50), (api, 10)]:
        series.append(input_series('go_goroutines', labels, '10+0x10'))
        series.append(input_series('process_open_fds', labels, f'{open_fds}+0x10'))
        series.append(input_series('process_max_fds', labels, '100+0x10'))
    series.append(input_series('kube_job_status_failed', {'namespace': 'one', 'job_name': 'migrate'}, '1+0x10'))
    app = {'namespace': 'one', 'app': 'worker'}
    checks = [fixture_check(service, panel, [({}, value)]) for panel, value in [(31, 1), (32, 2), (33, 1), (34, 2)]]
    checks += [fixture_check(service, 42, [(app, .25)], target='A'), fixture_check(service, 42, [(app, 0)], target='B'),
               fixture_check(service, 44, [(dict(app, queue_destination='emails'), 40)]),
               fixture_check(service, 64, [(app, .5), ({'namespace': 'one', 'app': 'api'}, .1)]),
               fixture_check(service, 54, [({'namespace': 'one', 'job_name': 'migrate'}, 1)]),
               fixture_check(service, 32, [], app='api')]
    return [{'interval': '1m', 'input_series': series, 'promql_expr_test': checks}]


def database_fixture_tests(database):
    series = []
    for replica in ['a', 'b']:
        db = {'instance': 'db-a', 'prometheus_replica': replica}
        for metric, values in [('mysql_up', '1+0x10'), ('mysql_global_status_threads_connected', '80+0x10'),
                               ('mysql_global_variables_max_connections', '100+0x10'), ('mysql_global_status_threads_running', '5+0x10'),
                               ('mysql_global_status_slow_queries', '0+6x10'), ('mysql_global_status_aborted_connects', '0+3x10')]:
            series.append(input_series(metric, db, values))
        series.append(input_series('mysql_global_status_commands_total', dict(db, command='select'), '0+60x10'))
        series.append(input_series('mysql_global_status_commands_total', dict(db, command='show_status'), '0+60x10'))
        series.append(input_series('mysql_global_status_connection_errors_total', dict(db, error='internal'), '0+3x10'))
    series.append(input_series('mysql_up', {'instance': 'db-b'}, '0+0x10'))
    series.append(input_series('mysql_slave_status_seconds_behind_source', {'instance': 'db-b'}, '12+0x10'))
    client = []
    for app, pod, used, limit in [('api', 'api-0', 9, 10), ('api', 'api-1', 1, 10), ('worker', 'worker-0', 2, 0)]:
        pool = {'namespace': 'one', 'app': app, 'pod_name': pod, 'container': 'api', 'db_name': 'primary'}
        client.append(input_series('go_sql_in_use_connections', pool, f'{used}+0x10'))
        client.append(input_series('go_sql_max_open_connections', pool, f'{limit}+0x10'))
    main = {'namespace': 'one', 'app': 'api', 'pod_name': 'api-0', 'container': 'api', 'db_name': 'primary'}
    client.append(input_series('go_sql_wait_count_total', main, '0+6x10'))
    client.append(input_series('database_sql_operations_total', dict(main, operation='select', result='success'), '0+54x10'))
    client.append(input_series('database_sql_operations_total', dict(main, operation='select', result='error'), '0+6x10'))
    for le in ['0.1', '+Inf']:
        client.append(input_series('database_sql_operation_duration_seconds_bucket', dict(main, operation='select', le=le), '0+60x10'))
    db_a = {'instance': 'db-a'}
    checks = [fixture_check(database, panel, [({}, value)]) for panel, value in [(201, 2), (202, 1), (203, .8), (204, 5), (205, .1), (206, 12)]]
    checks += [fixture_check(database, 208, [(db_a, .8)]), fixture_check(database, 209, [(dict(db_a, command='select'), 1)]),
               fixture_check(database, 211, [(dict(db_a, error='internal'), .05)], target='A'),
               fixture_check(database, 211, [(db_a, .05)], target='B')]
    api, worker = {'namespace': 'one', 'app': 'api', 'db_name': 'primary'}, {'namespace': 'one', 'app': 'worker', 'db_name': 'primary'}
    # 上限 0（无限）的连接池计入使用数，但不参与上限合计与利用率。
    for ref, expected in [('A', [(api, 10), (worker, 2)]), ('B', [(api, 20)]), ('C', [(api, .9)]), ('D', [(api, .1)]),
                          ('E', [(api, 1)]), ('F', [(api, .1)]), ('G', [(api, .095)])]:
        checks.append(fixture_check(database, 216, expected, target=ref))
    # 未接入 mysqld_exporter：服务端区为空（“未接入”），客户端区照常。
    unconnected = [fixture_check(database, panel, []) for panel in [201, 202, 203, 205, 206, 208]]
    unconnected.append(fixture_check(database, 216, [(api, 10), (worker, 2)], target='A'))
    return [{'interval': '1m', 'input_series': series + client, 'promql_expr_test': checks},
            {'interval': '1m', 'input_series': client, 'promql_expr_test': unconnected}]


def redis_fixture_tests(redis):
    series = []
    for instance, used, limit in [('redis-a', 900, 1000), ('redis-b', 100, 0)]:
        labels = {'instance': instance}
        series.append(input_series('redis_up', labels, '1+0x10'))
        series.append(input_series('redis_memory_used_bytes', labels, f'{used}+0x10'))
        series.append(input_series('redis_memory_max_bytes', labels, f'{limit}+0x10'))
    a = {'instance': 'redis-a'}
    for metric, values in [('redis_evicted_keys_total', '0+60x10'), ('redis_connected_clients', '42+0x10'),
                           ('redis_commands_processed_total', '0+120x10')]:
        series.append(input_series(metric, a, values))
    series.append(input_series('redis_commands_duration_seconds_total', dict(a, cmd='get'), '0+3x10'))
    series.append(input_series('redis_commands_total', dict(a, cmd='get'), '0+60x10'))
    pool = {'namespace': 'one', 'app': 'api', 'pod_name': 'api-0', 'container': 'api', 'db_system': 'redis',
            'redis_connection': 'primary', 'pool_name': 'default'}
    client = [input_series('db_client_connections_usage', dict(pool, state='used'), '3+0x10'),
              input_series('db_client_connections_waits', pool, '0+60x10'),
              input_series('db_client_connections_timeouts', pool, '0+0x10'),
              input_series('db_client_connections_use_time_milliseconds_count', dict(pool, type='command', status='success'), '0+48x10'),
              input_series('db_client_connections_use_time_milliseconds_count', dict(pool, type='command', status='error'), '0+12x10')]
    for le in ['100', '+Inf']:
        client.append(input_series('db_client_connections_use_time_milliseconds_bucket', dict(pool, type='command', status='success', le=le), '0+60x10'))
    checks = [fixture_check(redis, panel, [({}, value)]) for panel, value in [(301, 2), (302, 0), (303, .9), (304, 1), (305, .05), (306, 42)]]
    # 未设置 maxmemory 的实例不出使用率，不能显示为 0%。
    checks += [fixture_check(redis, 308, [(a, .9)]), fixture_check(redis, 312, [(dict(a, cmd='get'), .05)])]
    app = {'namespace': 'one', 'app': 'api', 'redis_connection': 'primary', 'pool_name': 'default'}
    for ref, value in [('A', 3), ('B', 1), ('C', 0), ('D', 1), ('E', .2), ('F', 95)]:
        checks.append(fixture_check(redis, 318, [(app, value)], target=ref))
    unconnected = [fixture_check(redis, panel, []) for panel in [301, 302, 303, 305, 308]]
    unconnected.append(fixture_check(redis, 318, [(app, 1)], target='B'))
    return [{'interval': '1m', 'input_series': series + client, 'promql_expr_test': checks},
            {'interval': '1m', 'input_series': client, 'promql_expr_test': unconnected}]


def messaging_fixture_tests(messaging):
    main = {'namespace': 'one', 'app': 'worker', 'pod_name': 'worker-0', 'container': 'api'}
    queue = dict(main, queue_destination='emails', queue_consumer='mailer')
    kafka = dict(main, kafka_destination='events', kafka_consumer='sync')
    consumers = [input_series('queue_consumer_messages_total', dict(queue, queue_result='success'), '0+45x10'),
                 input_series('queue_consumer_messages_total', dict(queue, queue_result='failed'), '0+15x10'),
                 input_series('kafka_consumer_messages_total', dict(kafka, kafka_result='success'), '0+54x10'),
                 input_series('kafka_consumer_messages_total', dict(kafka, kafka_result='error'), '0+6x10')]
    series = consumers + [input_series('kafka_consumer_dead_letters_total', dict(kafka, kafka_result='success'), '0+3x10'),
                          input_series('queue_consumer_failed_tasks_total', dict(queue, queue_result='success'), '0+3x10'),
                          input_series('up', {'foundation_kafka': 'true', 'kafka_cluster': 'primary', 'instance': 'exporter-a'}, '1+0x10')]
    checks = [fixture_check(messaging, panel, [({}, value)]) for panel, value in [(420, 1), (423, .25), (424, .1), (425, .1)]]
    # 未接入 kafka-exporter、未注册 RegisterStats：Lag 与年龄为空；有消费但没有死信时为 0，而不是“无消费者”。
    unconnected = [fixture_check(messaging, panel, []) for panel in [420, 421, 422]] + [fixture_check(messaging, 425, [({}, 0)])]
    return [{'interval': '1m', 'input_series': series, 'promql_expr_test': checks},
            {'interval': '1m', 'input_series': consumers, 'promql_expr_test': unconnected}]


def gateway_fixture_tests(gateway):
    gw = {'namespace': 'apps', 'app': 'gw', 'pod': 'gw-0'}
    inbound = []
    for code, step in [('200', 48), ('500', 6), ('429', 6)]:
        inbound.append(input_series('server_requests_code_total', dict(gw, operation='/api/orders', code=code), f'0+{step}x10'))
    for le in ['0.5', '+Inf']:
        inbound.append(input_series('server_requests_seconds_bucket', dict(gw, operation='/api/orders', le=le), '0+60x10'))
    outbound = [input_series('client_requests_code_total', dict(gw, operation='/orders.v1.Orders/Get', code='200'), '0+30x10'),
                input_series('client_requests_code_total', dict(gw, operation='/orders.v1.Orders/Get', code='503', reason='SERVICE_UNAVAILABLE'), '0+30x10'),
                input_series('client_requests_code_total', dict(gw, operation='/healthz', code='200'), '0+60x10')]
    for le in ['1', '+Inf']:
        outbound.append(input_series('client_requests_seconds_bucket', dict(gw, operation='/orders.v1.Orders/Get', le=le), '0+60x10'))
    route = {'namespace': 'apps', 'app': 'gw', 'operation': '/api/orders'}
    orders, health = {'namespace': 'apps', 'app': 'gw', 'downstream': 'orders.v1.Orders'}, {'namespace': 'apps', 'app': 'gw', 'downstream': '/healthz'}
    checks = [fixture_check(gateway, panel, [({}, value)]) for panel, value in [(2, 1), (3, .1), (4, .495), (5, .1), (6, .25), (7, .99)]]
    for ref, value in [('A', 1), ('B', .1), ('C', .1), ('D', .495)]:
        checks.append(fixture_check(gateway, 9, [(route, value)], target=ref))
    for ref, expected in [('A', [(orders, 1), (health, 1)]), ('B', [(orders, .5), (health, 0)]), ('C', [(orders, .99)])]:
        checks.append(fixture_check(gateway, 13, expected, target=ref))
    checks.append(fixture_check(gateway, 14, [(dict(orders, code='503', reason='SERVICE_UNAVAILABLE'), .5)]))
    # 只有入口流量的应用：下游区为空，不显示 0% 错误。
    inbound_only = [fixture_check(gateway, panel, []) for panel in [6, 7, 13]] + [fixture_check(gateway, 2, [({}, 1)])]
    return [{'interval': '1m', 'input_series': inbound + outbound, 'promql_expr_test': checks},
            {'interval': '1m', 'input_series': inbound, 'promql_expr_test': inbound_only}]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--container', help='使用现有容器中的 promtool；省略时用固定 Prometheus 镜像启动临时容器')
    args = parser.parse_args()
    paths = sorted((ROOT / 'grafana/dashboards').glob('*.json'))
    dashboards = [json.loads(p.read_text()) for p in paths]
    uids = [d['uid'] for d in dashboards]
    assert len(uids) == len(set(uids)), 'Dashboard UID 必须唯一'
    assert EXPECTED_UIDS <= set(uids), f'缺少约定的 Dashboard: {EXPECTED_UIDS - set(uids)}'
    by_uid = {d['uid']: d for d in dashboards}
    overview, components, service = by_uid['ack-container-overview'], by_uid['ack-application-components'], by_uid['ack-service-overview']
    shared, fleet, gateway = by_uid['foundation-shared-resources'], by_uid['foundation-fleet-overview'], by_uid['foundation-gateway']
    database, redis, messaging = by_uid['foundation-database'], by_uid['foundation-redis'], by_uid['foundation-messaging']
    # Folder 建议与打包共用 folders.json；业务页（90 业务）不进模板。
    folders = json.loads((ROOT / 'grafana/folders.json').read_text())
    listed = {name: f['folder'] for f in folders for name in f['dashboards']}
    assert sum(len(f['dashboards']) for f in folders) == len(listed), 'folders.json 中面板重复'
    assert set(listed) == {p.name for p in paths}, f'folders.json 与面板文件不一致: {set(listed) ^ {p.name for p in paths}}'
    assert {f['folder'] for f in folders} <= set(FOLDERS), f'Folder 须为 {FOLDERS}'
    for path, d in zip(paths, dashboards):
        assert listed[path.name] in d['tags'], f'{d["uid"]} 的 tags 须包含 Folder {listed[path.name]}'
    # 总览只读预聚合与 ALERTS，几十个应用时不扫原始序列。
    for p in panels(fleet):
        for t in p.get('targets', []):
            raw = {m for m in re.findall(r'([a-zA-Z_:][a-zA-Z0-9_:]*)\{', t['expr']) if ':' not in m} - {'ALERTS'}
            assert not raw, f'平台总览「{p["title"]}」直接查询原始指标: {raw}'
    empty_checks = []
    for d in dashboards:
        names = {v['name'] for v in d['templating']['list']}
        pod_label = next((v for v in d['templating']['list'] if v['name'] == 'app_pod_label'), None)
        if pod_label is not None:
            # 原始标签名参与 PromQL；可选值须保持白名单，且跨页链接须携带当前选择。
            assert pod_label['type'] == 'custom' and pod_label['hide'] == 0
            assert pod_label['skipUrlSync'] is False, '应用 Pod 标签来源必须同步到导航 URL'
            choices = [value.strip() for value in pod_label['query'].split(',')]
            assert len(choices) == 2 and set(choices) == {'pod_name', 'pod'}, '应用 Pod 标签仅支持 pod_name/pod'
            assert not pod_label.get('multi') and not pod_label.get('includeAll')
        # 所有页面顶部导航相同，覆盖全部约定页面。
        assert [link['url'] for link in d['links']] == ['/d/' + uid for uid in NAV_ORDER], f'{d["uid"]} 的顶部导航须与约定顺序一致'
        for link in d['links']:
            assert link['includeVars'] is True and link['keepTime'] is True, '跨页导航须保留变量和时间范围'
        # 面板链接、表格数据链接与 Markdown 中的跳转目标必须存在。
        targets = set(re.findall(r'/d/([A-Za-z0-9_-]+)', json.dumps([p for p in panels(d)], ensure_ascii=False)))
        assert targets <= set(uids), f'{d["uid"]} 的面板链接目标不存在: {targets - set(uids)}'
        # Grafana 内置数据链接变量来自当前字段，不属于 dashboard templating。
        referenced = {name for name in re.findall(r'\$\{(\w+)(?::\w+)?\}', json.dumps(d)) if not name.startswith('__')}
        assert referenced <= names, f'{d["uid"]} 未定义变量: {referenced - names}'
        assert not re.search(r'\$\{\w+:regex\}', json.dumps(d)), '使用 Prometheus 默认转义'
        ids = [p['id'] for p in panels(d)]
        assert len(ids) == len(set(ids))
        views = dashboard_views(d)
        for p in panels(d):
            for t in p.get('targets', []):
                assert 'foundation=' not in t['expr'] and '${instance}' not in t['expr']
                for mode in views:
                    expected = [{'labels': '{}', 'value': 0}] if 'or vector(0)' in t['expr'] else []
                    empty_checks.append({'expr': expression(t['expr'], mode), 'eval_time': '10m', 'exp_samples': expected})
    fixtures = (fixture_tests(overview) + node_fixture_tests(overview) + resource_state_fixture_tests(overview)
                + component_fixture_tests(merged(components, database, redis, messaging))
                + service_fixture_tests(service) + service_section_fixture_tests(service) + shared_fixture_tests(messaging, shared)
                + fleet_fixture_tests(fleet) + platform_fixture_tests(fleet) + database_fixture_tests(database)
                + redis_fixture_tests(redis) + messaging_fixture_tests(messaging) + gateway_fixture_tests(gateway))
    # 舰队页依赖 recording rules，测试与生产加载同一份规则文件，避免面板与规则口径漂移。
    rules = ROOT / 'prometheus/alerts.yaml'
    if args.container:
        rule_path = '/tmp/foundation-alerts.yaml'
        subprocess.run(['docker', 'cp', str(rules), f'{args.container}:{rule_path}'], check=True)
        cmd = ['docker', 'exec', '-i', args.container, 'promtool']
    else:
        rule_path = '/etc/prometheus/alerts.yaml'
        cmd = ['docker', 'run', '--rm', '-i', '-v', f'{rules.parent}:/etc/prometheus:ro', '--entrypoint', 'promtool', 'prom/prometheus:v3.5.5']
    tests = {'rule_files': [rule_path], 'evaluation_interval': '1m', 'tests': [{'interval': '1m', 'promql_expr_test': empty_checks}] + fixtures}
    subprocess.run(cmd + ['test', 'rules', '/dev/stdin'], input=json.dumps(tests), text=True, check=True)
    nonempty_checks = sum(len(test['promql_expr_test']) for test in fixtures)
    print(f'{len(dashboards)} 份面板的导航、Folder、链接、变量和 {len(empty_checks)} 条空样本查询检查通过；'
          f'{nonempty_checks} 条合成样本断言覆盖隔离、去重、Limit、组件单位、分母口径与 exporter 未接入')


if __name__ == '__main__':
    main()
