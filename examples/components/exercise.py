#!/usr/bin/env python3
"""真实请求驱动的指标增量核验；仅使用 Python 标准库，失败退出非零。"""
import argparse
import json
import math
from pathlib import Path
import time
from urllib.request import Request, urlopen
from urllib.parse import urlencode

PROM = 'http://127.0.0.1:19090'
APP = 'components-api'

def query(expr):
    with urlopen(PROM + '/api/v1/query?' + urlencode({'query': expr}), timeout=10) as r:
        data = json.load(r)
    if data['status'] != 'success':
        raise RuntimeError(data)
    return data['data']['result']

def snapshot():
    return query('{app="' + APP + '"}')

def total(samples, name, labels=None):
    labels = labels or {}
    return sum(float(s['value'][1]) for s in samples if s['metric'].get('__name__') == name and
               all(s['metric'].get(k) == v for k, v in labels.items()))

def expectations():
    checks = []
    def add(name, count, **labels):
        checks.append((name, labels, count))
    add('business_demo_runs_total', 1)
    for operation, result, count in [('create','success',1),('query','success',2),('update','success',1),('delete','success',1),('query','not_found',1),('raw','success',1)]:
        add('database_sql_operations_total', count, db_name='primary', operation=operation, result=result)
    for result, count in [('hit',3),('miss',1)]:
        add('business_cache_lookups_total', count, cache_name='orders', result=result)
    add('business_cache_loads_total', 1, cache_name='orders', result='success')
    for operation, result in [('lock','success'),('try_lock','contended'),('ttl','success'),('refresh','success'),('unlock','success')]:
        add('lock_operations_total', 1, lock_name='orders', operation=operation, result=result)
    add('lock_released_hold_duration_seconds_count', 1, lock_name='orders')
    for operation in ['put','stat','exists','get','delete']:
        add('oss_requests_total', 1, bucket='assets', operation=operation, result='success')
        add('oss_request_duration_seconds_count', 1, bucket='assets', operation=operation, result='success')
    for operation in ['put','get']:
        add('oss_transferred_bytes_total', len(b'Foundation components demo: real OSS bytes.\n'), bucket='assets', operation=operation)
    add('oss_streams_total', 1, bucket='assets', result='success')
    for domain, destination in [('queue','components-tasks'),('kafka','components-events')]:
        labels = {domain+'_destination': destination}
        add(domain+'_producer_messages_total', 3, **labels, **{domain+'_result':'success'})
        for result, count in [('success',2),('error',2)]:
            add(domain+'_consumer_attempts_total', count, **labels, **{domain+'_result':result})
        add(domain+'_consumer_retries_total', 1, **labels)
        add(domain+'_consumer_messages_total', 2, **labels, **{domain+'_result':'success'})
    add('queue_consumer_messages_total', 1, queue_destination='components-tasks', queue_result='failed')
    add('queue_consumer_failed_tasks_total', 1, queue_destination='components-tasks')
    add('kafka_consumer_messages_total', 1, kafka_destination='components-events', kafka_result='dead_lettered')
    add('kafka_consumer_dead_letters_total', 1, kafka_destination='components-events')
    add('kafka_producer_messages_total', 1, kafka_destination='components-deadletter', kafka_result='success')
    for state in ['ready','scheduled']:
        add('queue_tasks', 1, queue_destination='components-backlog', state=state)
    add('queue_tasks', 1, queue_destination='components-tasks', state='failed')
    add('client_requests_code_total', 1, operation='/example.Greeting/Hello', code='200')
    add('server_requests_code_total', 1, operation='/example.Components/Run', code='200')
    for operation in ['Catalog', 'Inventory']:
        for domain in ['server', 'client']:
            add(domain+'_requests_code_total', 1, operation='/example.Components/'+operation, code='200')
    for destination, consumer in [('components-email','email-worker'), ('components-report','report-worker')]:
        add('queue_producer_messages_total', 1, queue_destination=destination, queue_result='success')
        for metric in ['queue_consumer_messages_total', 'queue_consumer_attempts_total']:
            add(metric, 1, queue_destination=destination, queue_consumer=consumer, queue_result='success')
    return checks

def verify_reload():
    path = Path(__file__).parent / '.runtime' / 'config.yaml'
    original = path.read_text()
    before = snapshot()
    def publish(text):
        temporary = path.with_suffix('.next')
        temporary.write_text(text)
        temporary.replace(path)
    def wait_for(result, baseline):
        deadline = time.monotonic() + 45
        while time.monotonic() < deadline:
            value = total(snapshot(), 'foundation_config_updates_total', {'result':result})
            if value > baseline:
                return value-baseline
            time.sleep(2)
        raise RuntimeError('config update not observed: '+result)
    def wait_pool(expected):
        deadline = time.monotonic() + 45
        while time.monotonic() < deadline:
            if total(snapshot(), 'go_sql_max_open_connections', {'db_name':'primary'}) == expected:
                return
            time.sleep(2)
        raise RuntimeError('database pool hot update not applied: '+str(expected))
    try:
        publish(original.replace('max_open_conns: 8', 'max_open_conns: 9'))
        accepted = wait_for('accepted', total(before,'foundation_config_updates_total',{'result':'accepted'}))
        wait_pool(9)
        publish(original.replace('stop_delay: 3s', 'stop_delay: [invalid-yaml'))
        rejected = wait_for('rejected', total(before,'foundation_config_updates_total',{'result':'rejected'}))
    finally:
        publish(original)
    wait_pool(8)
    return {'accepted_delta':accepted,'rejected_delta':rejected,'pool_max_applied':9,'pool_max_restored':8}

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--rounds', type=int, default=5)
    args = parser.parse_args()
    if not 1 <= args.rounds <= 100:
        parser.error('--rounds must be 1..100')
    for _ in range(60):
        try:
            with urlopen('http://127.0.0.1:19011/readyz', timeout=3) as r:
                assert r.status == 200
            before = snapshot()
            if total(before, 'up', {'foundation':'true'}) == 1:
                break
        except (OSError, AssertionError):
            pass
        time.sleep(2)
    else:
        raise RuntimeError('components target did not become ready')
    # 等待一个采集周期，避免刚重启时将旧进程的末次样本当作基线。
    time.sleep(16)
    before = snapshot()
    started = time.time()
    run_ids = []
    for i in range(args.rounds):
        with urlopen(Request('http://127.0.0.1:18010/demo/run', data=b'', method='POST'), timeout=40) as r:
            result = json.load(r)
        run_ids.append(result['run_id'])
        print(f'round {i+1}: {result["run_id"]}', flush=True)
        if i+1 < args.rounds:
            time.sleep(5)
    checks = expectations()
    deadline = time.monotonic() + 90
    while True:
        after = snapshot()
        results = [{'metric':name, 'labels':labels, 'expected_delta':count*args.rounds,
                    'actual_delta':total(after,name,labels)-total(before,name,labels)} for name,labels,count in checks]
        failed = [r for r in results if r['actual_delta'] != r['expected_delta']]
        if not failed or time.monotonic() >= deadline:
            break
        time.sleep(3)
    for row in results:
        print(('PASS' if row not in failed else 'FAIL') + ' ' + json.dumps(row, ensure_ascii=False))
    # 基础运行状态及存在性单独校验，不将进程/后台采样误认为每轮固定增量。
    state = {}
    for name in ['go_goroutines','go_memstats_heap_alloc_bytes','process_resident_memory_bytes','process_virtual_memory_bytes',
                 'go_gc_duration_seconds_count','go_sched_latencies_seconds_count',
                 'db_client_connections_usage','db_client_connections_use_time_milliseconds_count',
                 'foundation_config_watcher_up','job_runs_total','queue_oldest_ready_age_seconds']:
        values = [float(s['value'][1]) for s in after if s['metric'].get('__name__') == name]
        if not values or any(not math.isfinite(v) or v < 0 for v in values):
            raise RuntimeError('missing or invalid runtime metric: '+name)
        state[name] = values
    for name in ['probe_success','queue_stats_collection_success','queue_stats_oldest_ready_known']:
        values = [float(s['value'][1]) for s in after if s['metric'].get('__name__') == name]
        if len(values) != (2 if name == 'probe_success' else 4) or any(v != 1 for v in values):
            raise RuntimeError('unexpected health/stat state: '+name+' '+str(values))
        state[name] = values
    lag = query('kafka_consumergroup_lag{kafka_cluster="components-kafka",consumergroup="components-demo",topic="components-events"}')
    if len(lag)!=1 or float(lag[0]['value'][1])!=0:
        raise RuntimeError('consumer should have committed all three scenarios: '+str(lag))
    state['kafka_lag'] = 0
    slow = total(after, 'database_sql_slow_operations_total', {'operation':'raw'}) - total(before, 'database_sql_slow_operations_total', {'operation':'raw'})
    if slow < args.rounds or total(after, 'database_sql_slow_threshold_seconds') != 0.01:
        raise RuntimeError('slow SQL threshold/count differs from expectation: '+str(slow))
    state['slow_sql_delta'] = slow
    state['operation_series'] = {}
    for metric, label, expected in [
        ('server_requests_code_total','operation', {'/example.Components/Run','/example.Components/Catalog','/example.Components/Inventory'}),
        ('client_requests_code_total','operation', {'/example.Greeting/Hello','/example.Components/Catalog','/example.Components/Inventory'}),
        ('job_runs_total','exported_job', {'redis-heartbeat','cache-size','cache-ttl'}),
        ('queue_consumer_messages_total','queue_consumer', {'components-demo','email-worker','report-worker'}),
    ]:
        observed = {s['metric'].get(label) for s in after if s['metric'].get('__name__') == metric and float(s['value'][1]) > 0}
        if observed != expected:
            raise RuntimeError('unexpected distinct series: '+metric+' '+str(observed))
        state['operation_series'][metric] = sorted(observed)
    window = max(30, int(time.time()-started)+1)
    state['p95_seconds'] = {}
    for metric in ['database_sql_operation_duration_seconds','oss_request_duration_seconds','lock_operation_duration_seconds','queue_consumer_attempt_duration_seconds','kafka_consumer_attempt_duration_seconds']:
        rows = query('histogram_quantile(0.95, sum by (le) (rate('+metric+'_bucket{app="'+APP+'"}['+str(window)+'s])))')
        if not rows or not math.isfinite(float(rows[0]['value'][1])) or float(rows[0]['value'][1]) < 0:
            raise RuntimeError('invalid latency quantile: '+metric)
        state['p95_seconds'][metric] = float(rows[0]['value'][1])
    state['mean_seconds'] = {}
    for name in state['p95_seconds']:
        # SQL 使用原生 Prometheus Histogram；其现有首桶为 5ms，不经过 OTel View。
        boundary = '0.005' if name == 'database_sql_operation_duration_seconds' else '0.001'
        if not any(x['metric'].get('__name__') == name+'_bucket' and x['metric'].get('le') == boundary for x in after):
            raise RuntimeError('missing seconds-scale histogram buckets: '+name)
        count = total(after,name+'_count')-total(before,name+'_count')
        elapsed = total(after,name+'_sum')-total(before,name+'_sum')
        state['mean_seconds'][name] = elapsed/count
    state['config_reload'] = verify_reload()
    report = {'rounds':args.rounds,'run_ids':run_ids,'checks':results,'state':state,'passed':not failed}
    out = Path(__file__).parent / '.runtime' / 'latest-report.json'
    out.parent.mkdir(exist_ok=True)
    out.write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
    print('report: '+str(out))
    if failed:
        raise SystemExit(f'{len(failed)} metric checks failed')
    print(f'PASS: {len(checks)} exact delta checks and runtime/health/lag checks')

if __name__ == '__main__':
    main()
