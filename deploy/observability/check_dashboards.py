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
    return [{'labels': '{' + ','.join(k + '=' + json.dumps(v) for k, v in labels.items()) + '}', 'value': value}
            for labels, value in expected]


def fixture_check(dashboard, panel_id, expected, view='view_pod', target=0, **filters):
    panel = next(p for p in panels(dashboard) if p['id'] == panel_id)
    return {'expr': expression(panel['targets'][target]['expr'], view, **filters),
            'eval_time': '10m', 'exp_samples': expected_samples(expected)}


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
    checks.append(fixture_check(service, 3, [({}, 0)], app='api'))
    checks.append(fixture_check(service, 13, [({'namespace': 'one', 'app': 'worker', 'operation': '/users', 'kind': 'http'}, .5)]))
    checks.append(fixture_check(service, 17, [({'namespace': 'one', 'app': 'api', 'pod': 'orphan-0', 'container': 'api',
                                             'instance': '10.0.0.3:9000', 'job': 'application'}, 1)]))
    zero = [item for item in series if item['series'].startswith('kube_pod_info')]
    zero.append(input_series('server_requests_code_total', {'namespace': 'one', 'app': 'api', 'pod_name': 'api-0', 'container': 'api',
                                                           'operation': '/users', 'kind': 'http', 'code': '200'}, '0+0x10'))
    return [{'interval': '1m', 'input_series': series, 'promql_expr_test': checks},
            {'interval': '1m', 'input_series': zero, 'promql_expr_test': [fixture_check(service, 3, []), fixture_check(service, 10, [])]}]


def shared_fixture_tests(shared):
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
    cases = [(2, [(dict(queue, state='ready'), 12)]), (3, [(queue, 50)]), (4, [(queue, 0)]), (5, [(queue, 0)]),
             (8, [(kafka, 18)]), (10, [(dict(kafka, partition='2'), -1)]),
             (13, [(probe, 0)]), (14, [(probe, 1)]), (15, [(probe, 503)]), (16, [(probe, .25)])]
    checks = [fixture_check(shared, panel, expected, env='qa', broker_topic='events') for panel, expected in cases]
    unknown_labels = dict(queue, namespace='one', app='worker', instance='unknown')
    unknown = [input_series(metric, unknown_labels, f'{value}+0x10') for metric, value in
               [('queue_oldest_ready_age_seconds', 999), ('queue_stats_collection_success', 1), ('queue_stats_oldest_ready_known', 0)]]
    return [{'interval': '1m', 'input_series': series, 'promql_expr_test': checks},
            {'interval': '1m', 'input_series': unknown, 'promql_expr_test': [fixture_check(shared, 3, [])]}]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--container', help='使用现有容器中的 promtool；省略时用固定 Prometheus 镜像启动临时容器')
    args = parser.parse_args()
    dashboards = [json.loads(p.read_text()) for p in sorted((ROOT / 'grafana/dashboards').glob('*.json'))]
    uids = [d['uid'] for d in dashboards]
    expected_uids = {'ack-service-overview', 'ack-container-overview', 'ack-application-components', 'foundation-shared-resources'}
    assert len(uids) == len(set(uids)), 'Dashboard UID 必须唯一'
    assert expected_uids <= set(uids), '缺少约定的四份 Dashboard'
    overview = next(d for d in dashboards if d['uid'] == 'ack-container-overview')
    components = next(d for d in dashboards if d['uid'] == 'ack-application-components')
    service = next(d for d in dashboards if d['uid'] == 'ack-service-overview')
    shared = next(d for d in dashboards if d['uid'] == 'foundation-shared-resources')
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
        linked_uids = set()
        for link in d['links']:
            match = re.fullmatch(r'/d/([^/?#]+)', link['url'])
            assert match and match[1] in set(uids), f'{d["uid"]} 的导航目标不存在: {link["url"]}'
            assert link['includeVars'] is True and link['keepTime'] is True, '跨页导航须保留变量和时间范围'
            linked_uids.add(match[1])
        assert expected_uids <= linked_uids, f'{d["uid"]} 缺少四页导航链接'
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
    fixtures = (fixture_tests(overview) + node_fixture_tests(overview) + resource_state_fixture_tests(overview) + component_fixture_tests(components)
                + service_fixture_tests(service) + shared_fixture_tests(shared))
    tests = {'rule_files': [], 'evaluation_interval': '1m', 'tests': [{'interval': '1m', 'promql_expr_test': empty_checks}] + fixtures}
    cmd = ['docker', 'exec', '-i', args.container, 'promtool'] if args.container else ['docker', 'run', '--rm', '-i', '--entrypoint', 'promtool', 'prom/prometheus:v3.5.5']
    subprocess.run(cmd + ['test', 'rules', '/dev/stdin'], input=json.dumps(tests), text=True, check=True)
    nonempty_checks = sum(len(test['promql_expr_test']) for test in fixtures)
    print(f'JSON、变量和 {len(empty_checks)} 条空样本查询检查通过；{nonempty_checks} 条合成样本断言覆盖隔离、去重、Limit、组件单位与分母口径')


if __name__ == '__main__':
    main()
