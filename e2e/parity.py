#!/usr/bin/env python3
"""Read-only API/MCP parity capture; outputs contain private application data.

Use the same frozen data, browser session, origin, UI, version and day for both
builds. Disable background jobs only in scratch harness builds. This script
never calls a tool without the server's readOnlyHint. Compare output directories
with diff -r. Only dashboard_render's measured timing_ms/render_ms and dashboard-view
body.as_of (cache build time, not a ledger valuation date) are omitted;
request IDs are fixed. tools/list captures the read-only registry only. No application fields, errors or array order are removed.
"""
import argparse
import base64
import hashlib
import json
import os
import subprocess
from pathlib import Path
import urllib.error
import urllib.parse
import urllib.request


def canonical(value):
    return json.dumps(value, sort_keys=True, ensure_ascii=False, indent=2) + '\n'


class Capture:
    def __init__(self, base, session, out, as_of):
        self.base = base.rstrip('/')
        self.out = Path(out)
        self.out.mkdir(mode=0o700, parents=True, exist_ok=False)
        self.as_of = as_of
        self.cookie = session['cookie']
        self.headers = {'Cookie': 'moneybag_session=' + session['cookie'],
                        'X-CSRF-Token': session['csrf'], 'Origin': os.environ.get('MONEYBAG_PARITY_ORIGIN', self.base),
                        'Content-Type': 'application/json',
                        'Accept': 'application/json, text/event-stream',
                        'MCP-Protocol-Version': '2025-11-25'}
        self.calls = []
        self.readonly = set()
        self.called = set()

    def save(self, name, value):
        (self.out / (name + '.json')).write_text(canonical(value))

    def request(self, path, body=None):
        req = urllib.request.Request(self.base + path, headers=self.headers,
                                     data=None if body is None else json.dumps(body).encode())
        try:
            response = urllib.request.urlopen(req, timeout=180)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            raw = response.read()
            try:
                value = json.loads(raw)
            except json.JSONDecodeError:
                value = raw.decode()
            return {'status': response.status, 'body': value}

    def get(self, name, path):
        result = self.request(path)
        self.calls.append({'name': name, 'method': 'GET', 'path': path})
        if name.startswith('api-dashboard-view-') and isinstance(result['body'], dict):
            result['body'].pop('as_of', None)
        self.save(name, result)
        return result['body']

    def rpc(self, method, params):
        return self.request('/api/mcp', {'jsonrpc': '2.0', 'id': 1,
                                       'method': method, 'params': params})

    def tool(self, name, args=None, suffix=''):
        if name not in self.readonly:
            raise ValueError('Tool is absent or not read-only: ' + name)
        self.called.add(name)
        result = self.rpc('tools/call', {'name': name, 'arguments': args or {}})
        envelope = result['body']
        body = envelope.get('result', {})
        # MCP duplicates structured JSON as a text block; canonicalize both.
        if name == 'dashboard_render' and isinstance(body.get('structuredContent'), dict):
            for key in ('timing_ms', 'render_ms'):
                body['structuredContent'].pop(key, None)
        for index, content in enumerate(body.get('content', [])):
            if content.get('type') == 'text':
                try:
                    data = json.loads(content['text'])
                except json.JSONDecodeError:
                    continue
                if name == 'dashboard_render' and isinstance(data, dict):
                    for key in ('timing_ms', 'render_ms'):
                        data.pop(key, None)
                content['text'] = canonical(data)
            elif content.get('type') == 'image':
                # Compare actual PNG bytes too, rather than discard screenshots.
                data = base64.b64decode(content['data'], validate=True)
                filename = 'tool-' + name + suffix + '-' + str(index) + '.png'
                (self.out / filename).write_bytes(data)
                content['data'] = {'sha256': hashlib.sha256(data).hexdigest()}
        filename = 'tool-' + name + suffix
        self.calls.append({'name': filename, 'tool': name, 'arguments': args or {}})
        self.save(filename, result)
        return body.get('structuredContent', {})

    def pages(self, tool, args, suffix, items='items', cursor='next_cursor'):
        found = []
        seen = set()
        for page in range(10000):
            value = self.tool(tool, args, suffix + '-page-' + str(page))
            found.extend(value.get(items) or [])
            next_cursor = value.get(cursor)
            if not next_cursor:
                return found
            if str(next_cursor) in seen:
                raise ValueError('Repeated pagination cursor: ' + tool)
            seen.add(str(next_cursor))
            args = dict(args, cursor=next_cursor)
        raise ValueError('Pagination did not terminate: ' + tool)

    def run(self):
        session = self.get('api-session', '/api/session')
        if not isinstance(session, dict) or not session.get('user'):
            raise ValueError('A valid browser test session is required')
        tools = self.rpc('tools/list', {})
        tools['body']['result']['tools'] = [t for t in tools['body']['result']['tools']
                                           if t.get('annotations', {}).get('readOnlyHint')]
        self.save('tools-list', tools)
        self.readonly = {t['name'] for t in tools['body']['result']['tools']
                         if t.get('annotations', {}).get('readOnlyHint')}
        self.get('healthz', '/healthz')
        for name, path in [('bootstrap', '/api/bootstrap'), ('cube', '/api/cube'),
                           ('saved-views', '/api/saved-views'), ('expr-catalog', '/api/expr/catalog'),
                           ('imports', '/api/imports'), ('imports-count', '/api/imports/count'),
                           ('reconcile-count', '/api/reconcile/count'), ('settings', '/api/settings'),
                           ('activity-agents', '/api/activity/agents'), ('consistency', '/api/consistency'),
                           ('changes', '/api/changes?routine=true')]:
            self.get('api-' + name, path)
        simple = ['agent_list', 'audit_log', 'catalog_get', 'categories_list',
                  'consistency_check', 'dashboard_list', 'dashboard_shares_list',
                  'db_list', 'db_templates', 'feedback_list', 'imports_list',
                  'metric_list', 'model_get', 'rates_sync_status', 'reconcile_status',
                  'rules_list', 'shared_with_me_list', 'usage_get', 'user_list', 'view_list', 'whoami']
        values = {name: self.tool(name) for name in simple}
        accounts = self.pages('accounts_list', {'include_closed': True, 'limit': 500}, '')
        self.tool('balances_get', {'at': self.as_of})
        self.tool('budget_get', {'month': self.as_of[:7]})
        self.tool('duplicates_find', {'from': '2000-01-01', 'to': self.as_of})
        entries = self.pages('entries_list', {'limit': 500, 'to': self.as_of}, '')
        # Every entry detail, including entries outside the first page.
        for index, entry in enumerate(entries):
            self.tool('entry_get', {'id': entry['id']}, '-' + str(index))
        if entries:
            self.tool('history_of', {'target_id': entries[0]['id'], 'include_payload': True})
        else:
            self.tool('history_of', {'target_id': 'parity-missing'})
        for index, account in enumerate(accounts):
            self.pages('entries_list', {'account': account['id'], 'limit': 500, 'to': self.as_of}, '-account-' + str(index))
            self.tool('balances_get', {'at': self.as_of, 'accounts': [account['id']]}, '-account-' + str(index))
            self.tool('reconcile_status', {'account': account['id']}, '-account-' + str(index))
        self.tool('expr_eval', {'expr': '1 + 1'})
        for index, dimension in enumerate(values['catalog_get'].get('dimensions', [])):
            self.tool('field_values', {'dimension': dimension, 'limit': 500}, '-' + str(index))
        self.tool('income_reconcile', {'from': '2000-01-01', 'to': self.as_of})
        self.tool('rates_get', {'pairs': ['EUR/USD', 'USD/EUR', 'XAU_g/USD'], 'at': self.as_of + 'T23:59:59Z'})
        self.tool('whatif_asset_shock', {'shocks': [{'commodity': 'XAU_g', 'pct': -10}]})
        rules = values['rules_list'].get('items') or []
        self.tool('rule_preview', {'rule_id': rules[0]['id']} if rules else
                  {'rule': {'priority': 1, 'match': 'false', 'set': {'kind': 'expense'}}})
        # Empty datasets still exercise the same not-found contract explicitly.
        self.tool('reconcile_suggest', {'discrepancy_id': 'parity-missing'})
        for index, table in enumerate(values['db_list'].get('items') or []):
            self.pages('db_query', {'db': table['id'], 'limit': 5000}, '-' + str(index))
            self.tool('db_query', {'db': table['id'], 'limit': 5000, 'join_entry': True}, '-joined-' + str(index))
        for index, scenario in enumerate(values['model_get'].get('scenarios') or []):
            self.tool('forecast_run', {'scenario_ids': [scenario['id']], 'horizon_months': 12, 'as_of': self.as_of}, '-' + str(index))
            self.tool('scenario_compare', {'scenario_ids': [scenario['id']], 'as_of': self.as_of}, '-' + str(index))
        for index, metric in enumerate(values['metric_list'].get('items') or []):
            self.tool('metric_query', {'metric': metric['id'], 'from': '2000-01-01', 'to': self.as_of}, '-' + str(index))
        for index, view in enumerate(values['view_list'].get('items') or []):
            self.tool('view_get', {'id': view['id']}, '-' + str(index))
        for index, batch in enumerate(values['imports_list'].get('items') or []):
            self.pages('import_preview', {'batch_id': batch['id']}, '-' + str(index), items='rows', cursor='next_cursor')
        for index, dashboard in enumerate(values['dashboard_list'].get('items') or []):
            suffix = '-' + str(index)
            id = dashboard['id']
            self.get('api-dashboard-view' + suffix, '/api/dashboards/' + urllib.parse.quote(id, safe='') + '/view')
            for name in ['dashboard_get', 'dashboard_lint', 'dashboard_summary', 'dashboard_test', 'dashboard_render']:
                self.tool(name, {'id': id}, suffix)
            for name in ['dashboard_preview', 'dashboard_validate']:
                self.tool(name, {'dashboard_id': id}, suffix)
            self.tool('dashboard_diff', {'dashboard_id': id, 'from': 'published', 'to': 'draft'}, suffix)
        shares = values['shared_with_me_list'].get('items') or []
        for index, share in enumerate(shares):
            id = urllib.parse.quote(share['id'], safe='')
            for suffix in ['', '/draft', '/catalog']:
                self.get('api-shared-' + str(index) + suffix.replace('/', '-'), '/api/shared/' + id + suffix)
            rendered = subprocess.run(['node', str(Path(__file__).with_name('parity-shared.mjs'))],
                                      input=json.dumps({'base': self.base, 'cookie': self.cookie,
                                                        'id': share['id'], 'readonly': sorted(self.readonly),
                                                        'origin': self.headers['Origin']}),
                                      text=True, capture_output=True, timeout=180, check=True)
            self.save('shared-html-' + str(index), json.loads(rendered.stdout))
        missing = self.readonly - self.called
        if missing:
            raise ValueError('Read-only tools without a fixture: ' + ', '.join(sorted(missing)))
        self.save('requests', self.calls)
        print('Captured', len(self.calls), 'requests;', len(self.called), 'read-only tools;',
              len(accounts), 'accounts;', len(entries), 'entries.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', required=True)
    parser.add_argument('--session', required=True, help='private JSON with cookie and csrf')
    parser.add_argument('--out', required=True, help='new private output directory')
    parser.add_argument('--as-of', required=True, help='fixed YYYY-MM-DD shared by both captures')
    args = parser.parse_args()
    os.umask(0o077)
    Capture(args.base, json.loads(Path(args.session).read_text()), args.out, args.as_of).run()


if __name__ == '__main__':
    main()
