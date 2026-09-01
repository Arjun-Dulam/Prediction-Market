#!/usr/bin/env python3
"""Destructive two-owner checks; use ONLY with a disposable Compose project."""
import argparse
import concurrent.futures
import json
import pathlib
import subprocess
import time
import urllib.error
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument('--project', required=True)
parser.add_argument('--base', default='http://localhost:8080')
args = parser.parse_args()
root = pathlib.Path(__file__).resolve().parents[1] / 'go-backend'
compose = ['docker', 'compose', '-p', args.project, '-f', 'docker-compose.yml',
           '-f', 'docker-compose.partitioned.yml']
def command(*parts):
    return subprocess.check_output(compose + list(parts), cwd=root, text=True)
def http(method, path, token='', data=None, want=200):
    request = urllib.request.Request(args.base + path, method=method,
        data=None if data is None else json.dumps(data).encode(),
        headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + token})
    try:
        with urllib.request.urlopen(request, timeout=20) as response:
            raw = response.read()
            assert response.status == want, (path, response.status, raw)
            return json.loads(raw) if raw else None
    except urllib.error.HTTPError as error:
        assert error.code == want, (path, error.code, error.read())
        return None
stamp = str(time.time_ns())
users = [http('POST', '/api/v1/users/register', data={
    'username': 'partition-' + stamp + '-' + str(i),
    'email': 'partition-' + stamp + '-' + str(i) + '@example.com',
    'password': 'partition-test-password'}, want=201) for i in range(2)]
for u in users:
    http('POST', '/api/v1/balances/deposit', u['token'], {'cents': 1000}, 204)
markets = ['partition-' + stamp + '-' + suffix for suffix in ['A', 'B']]
symbols = ['PARTITION_' + stamp + '_' + suffix for suffix in ['A', 'B']]
for market, symbol in zip(markets, symbols):
    http('POST', '/api/v1/markets', users[0]['token'], {'id': market, 'symbol': symbol}, 201)
def order(m, suffix, side, price, quantity):
    return {'id': markets[m] + '-' + suffix, 'market_id': markets[m],
            'side': side, 'price': price, 'quantity': quantity}
def place(data, user=0, want=201):
    return http('POST', '/api/v1/trading/orders', users[user]['token'], data, want)
def get_order(data, user=0):
    return http('GET', '/api/v1/trading/orders/' + data['id'], users[user]['token'])
def balances():
    return [http('GET', '/api/v1/balances/' + u['user_id'], u['token'])['cents'] for u in users]
makers = [order(m, 'maker', 'buy_yes', 60, 2) for m in range(2)]
for o in makers: place(o)
for m in range(2): place(order(m, 'taker', 'buy_no', 40, 1), 1)
assert balances() == [760, 920], balances()
for m in range(2):
    for u, expected in [(0, {'yes': 1, 'no': 0}), (1, {'yes': 0, 'no': 1})]:
        assert http('GET', '/api/v1/positions/' + users[u]['user_id'] + '/' + markets[m], users[u]['token']) == expected
# Reject collective overdraw, including requests to different owners.
orders = [order(i % 2, 'overdraw-' + str(i), 'buy_yes', 80, 5) for i in range(2)]
with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
    def attempt(o):
        request = urllib.request.Request(args.base + '/api/v1/trading/orders', method='POST',
            data=json.dumps(o).encode(), headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + users[0]['token']})
        try:
            with urllib.request.urlopen(request, timeout=20) as r:
                r.read(); return r.status
        except urllib.error.HTTPError as e:
            e.read(); return e.code
    statuses = list(pool.map(attempt, orders))
assert sorted(statuses) == [201, 400], statuses
accepted = orders[statuses.index(201)]
http('DELETE', '/api/v1/trading/orders/' + accepted['id'], users[0]['token'], want=204)
assert balances() == [760, 920]
# Retry one order across HTTP clients without repeating its reservation.
with concurrent.futures.ThreadPoolExecutor(max_workers=32) as pool:
    list(pool.map(lambda _: place(makers[0]), range(32)))
assert balances() == [760, 920]
for m in range(2):
    quote = http('GET', '/api/v1/markets/' + symbols[m] + '/quote')
    assert quote['bid'] == 60 and quote['ask'] == -1 and int(quote['sequence']) > 0, quote
before = [{k: v for k, v in get_order(o).items() if k != 'engine_id'} for o in makers]
command('kill', '-s', 'SIGKILL', 'api', 'engine', 'engine_b')
command('up', '-d', '--wait', 'engine', 'engine_b', 'api')
assert balances() == [760, 920]
assert before == [{k: v for k, v in get_order(o).items() if k != 'engine_id'} for o in makers]
# Kill one owner. Its uncertain submission must freeze the entire shared ledger.
def owner(symbol):
    h = 2166136261
    for b in symbol.encode(): h = ((h ^ b) * 16777619) & 0xffffffff
    return h % 2
m = next(i for i, s in enumerate(symbols) if owner(s) == 0)
command('kill', '-s', 'SIGKILL', 'engine')
uncertain = order(m, 'uncertain', 'buy_yes', 20, 1)
place(uncertain, want=503)
http('GET', '/health', want=503)
command('kill', '-s', 'SIGKILL', 'api', 'engine_b')
command('up', '-d', '--wait', 'engine', 'engine_b', 'api')
assert balances() == [740, 920], balances()
place(uncertain)
assert balances() == [740, 920]
http('DELETE', '/api/v1/trading/orders/' + uncertain['id'], users[0]['token'], want=204)
# Cancel one remaining maker on each owner and verify exact refunds.
for o in makers:
    http('DELETE', '/api/v1/trading/orders/' + o['id'], users[0]['token'], want=204)
assert balances() == [880, 920], balances()
for m in range(2):
    quote = http('GET', '/api/v1/markets/' + symbols[m] + '/quote')
    assert quote['bid'] == -1 and quote['ask'] == -1, quote
print(json.dumps({'engine_instances': 2, 'distinct_owners': [owner(s) for s in symbols],
    'shared_wallet_no_overdraw': True, 'parallel_retries': 32,
    'two_owner_SIGKILL_recovery': True, 'single_owner_failure_freezes_ledger': True,
    'uncertain_reservation_recovered_once': True, 'post_recovery_cancel_refunds': True,
    'versioned_quotes_after_cancel': True}))
