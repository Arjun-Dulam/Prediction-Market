#!/usr/bin/env python3
"""Destructive crash check for a disposable Compose project, never a shared stack.
Requires the same BLUEPRINT_DB_* environment as docker compose and default
exchange database/user for the independent SQL accounting check.
"""
import argparse
import concurrent.futures
import json
import pathlib
import subprocess
import time
import urllib.error
import urllib.request
import uuid

parser = argparse.ArgumentParser()
parser.add_argument('--base', default='http://localhost:8080')
parser.add_argument('--project', required=True, help='disposable Compose project to SIGKILL')
parser.add_argument('--partitioned', action='store_true', help='include both engine owners and their Compose override')
args = parser.parse_args()
compose = ['docker', 'compose', '-p', args.project]
if args.partitioned:
    compose += ['-f', 'docker-compose.yml', '-f', 'docker-compose.partitioned.yml']
engines = ['engine', 'engine_b'] if args.partitioned else ['engine']
root = pathlib.Path(__file__).resolve().parents[1] / 'go-backend'

def command(*parts):
    return subprocess.check_output(compose + list(parts), cwd=root, text=True)

def http(method, path, token='', payload=None, status=200):
    body = None if payload is None else json.dumps(payload).encode()
    request = urllib.request.Request(args.base + path, data=body, method=method,
        headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + token})
    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            raw = response.read()
            assert response.status == status, (path, response.status)
            return json.loads(raw) if raw else None
    except urllib.error.HTTPError as error:
        if error.code != status:
            raise AssertionError((path, error.code, error.read().decode())) from error

stamp = str(time.time_ns())
users = [http('POST', '/api/v1/users/register', payload={
    'username': 'crash-' + stamp + '-' + str(i),
    'email': 'crash-' + stamp + '-' + str(i) + '@example.com',
    'password': 'recovery-test-password'}, status=201) for i in range(2)]
market = 'crash-' + stamp
token = users[0]['token']
http('POST', '/api/v1/markets', token, {'id': market, 'symbol': 'CRASH_' + stamp}, 201)
for user in users:
    http('POST', '/api/v1/balances/deposit', user['token'], {'cents': 10000}, 204)
ids = []
for i, (side, price, quantity) in enumerate([('buy_yes', 60, 10), ('buy_no', 40, 10), ('buy_yes', 20, 3), ('buy_yes', 20, 5), ('buy_yes', 21, 2)]):
    order = {'id': market + '-' + str(i), 'market_id': market,
             'side': side, 'price': price, 'quantity': quantity}
    http('POST', '/api/v1/trading/orders', users[1 if i == 1 else 0]['token'], order, 201)
    ids.append(order['id'])
# Parallel retries traverse HTTP authentication and the real durability path.
with concurrent.futures.ThreadPoolExecutor(max_workers=32) as pool:
    list(pool.map(lambda _: http('POST', '/api/v1/trading/orders', token, order, 201), range(32)))
http('POST', '/api/v1/trading/orders', token, dict(order, quantity=4), 400)

def state():
    return {
        'balances': [http('GET', '/api/v1/balances/' + u['user_id'], u['token']) for u in users],
        'positions': [http('GET', '/api/v1/positions/' + u['user_id'] + '/' + market, u['token']) for u in users],
        'orders': [{k: v for k, v in http('GET', '/api/v1/trading/orders/' + oid,
                    users[1 if i == 1 else 0]['token']).items() if k != 'engine_id'} for i, oid in enumerate(ids)]}

before = state()
assert before['balances'] == [{'cents': 9198}, {'cents': 9600}], before
assert before['positions'] == [{'yes': 10, 'no': 0}, {'yes': 0, 'no': 10}], before
command('kill', '-s', 'SIGKILL', 'api', *engines)
command('up', '-d', '--wait', *engines, 'api')
after = state()
assert before == after, (before, after)
# Crossing order consumes the better price, then the earliest same-price order.
http('POST', '/api/v1/trading/orders', users[1]['token'], {
    'id': market + '-cross', 'market_id': market, 'side': 'buy_no',
    'price': 80, 'quantity': 5}, 201)
for index, expected in [(4, 'filled'), (2, 'filled'), (3, 'open')]:
    recovered = http('GET', '/api/v1/trading/orders/' + ids[index], token)
    assert recovered['status'] == expected, recovered
http('DELETE', '/api/v1/trading/orders/' + ids[3], token, status=204)
assert http('GET', '/api/v1/balances/' + users[0]['user_id'], token) == {'cents': 9298}
# Read independently projected PostgreSQL state, using generated UUIDs only.
for i, user in enumerate(users):
    uid = str(uuid.UUID(user['user_id']))
    sql = "SELECT json_build_object('cents',b.cents,'yes',p.yes_shares,'no',p.no_shares) FROM balances b JOIN positions p ON p.user_id=b.user_id WHERE b.user_id='" + uid + "' AND p.market_id='" + market + "'"
    projected = json.loads(command('exec', '-T', 'psql_bp', 'psql', '-U', 'exchange', '-d', 'exchange', '-Atc', sql))
    assert projected == [{'cents': 9298, 'yes': 15, 'no': 0}, {'cents': 9202, 'yes': 0, 'no': 15}][i], projected
print(json.dumps({'engine_instances': len(engines), 'parallel_retries': 32, 'crash': 'SIGKILL API and C++ engine',
    'account_state_equal': before == after, 'post_recovery_price_time_priority': True, 'post_recovery_cancel': True, 'postgres_matches_api': True}))
