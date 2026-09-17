#!/usr/bin/env python3
"""Check the local MCP protocol; optional read-only live order investigation."""
import argparse
import json
import pathlib
import subprocess

ROOT = pathlib.Path(__file__).resolve().parents[1]
p = argparse.ArgumentParser()
p.add_argument('--binary', default=str(ROOT / 'bin/logs-mcp'))
p.add_argument('--live-order-sn')
p.add_argument('--from-time', type=int)
p.add_argument('--to-time', type=int)
args = p.parse_args()
requests = [
    dict(id=1, method='initialize', params=dict(protocolVersion='2024-11-05', capabilities={}, clientInfo=dict(name='analysis-smoke', version='1'))),
    dict(id=2, method='tools/list', params={}),
    dict(id=3, method='resources/read', params=dict(uri='logs://price-audit-rules')),
]
if args.live_order_sn:
    if not args.from_time or not args.to_time:
        p.error('live verification requires --from-time and --to-time')
    requests.append(dict(id=4, method='tools/call', params=dict(name='order_price_history', arguments=dict(order_sn=args.live_order_sn, from_time=args.from_time, to_time=args.to_time, max_requests=30))))
wire = ''.join(json.dumps(dict(jsonrpc='2.0', **r)) + '\n' for r in requests)
proc = subprocess.run([args.binary], input=wire, text=True, capture_output=True, cwd=ROOT, timeout=180)
if proc.returncode:
    raise SystemExit('MCP process failed; raw stderr omitted')
responses = {r['id']: r for line in proc.stdout.splitlines() if line.strip() for r in [json.loads(line)]}
assert all('error' not in r for r in responses.values()), 'MCP protocol error'
tools = {t['name'] for t in responses[2]['result']['tools']}
expected = {'sls_query_all', 'order_price_history', 'order_price_audit', 'order_context', 'resolve_user_emails'}
assert expected <= tools, 'new tools missing'
assert responses[3]['result']['contents'][0]['text'], 'resource missing'
print(json.dumps(dict(version=responses[1]['result']['serverInfo']['version'], tools=len(tools), added_tools=sorted(expected), resource='ok')))
if args.live_order_sn:
    result = responses[4]['result']
    if result.get('isError'):
        raise SystemExit('Live investigation returned an error; raw details omitted')
    report = json.loads(result['content'][0]['text'])
    out = ROOT / 'outputs' / 'mcp-analysis-validation'
    out.mkdir(parents=True, exist_ok=True)
    path = out / 'live-history.json'
    path.write_text(json.dumps(report, ensure_ascii=False, indent=2))
    print(json.dumps(dict(live=True, all_pages_fetched=report['all_pages_fetched'], snapshots=report['snapshot_count'], initial_quote_present=report['initial_quote'] is not None, classification=report['classification'], missing_evidence=report['missing_evidence']), ensure_ascii=False))
    assert report['all_pages_fetched'], 'live collection incomplete'
    assert report['latest_log_snapshot'] is not None, 'no live order snapshot found'
