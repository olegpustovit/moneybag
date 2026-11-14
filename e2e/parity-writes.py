#!/usr/bin/env python3
"""Writes ONLY to disposable restored-copy harnesses, after read-only captures.

Compare new output directories with diff -r. Generated ULIDs are checked and
mapped consistently across subsequent references. Only new-entry created_at,
OAuth client_id_issued_at, newly issued random credentials, and new audit
at/outcome.at/hash/prev_hash/outcome.hash are normalized. Run TestParityAuditChains
on both write clones before accepting a diff with those derived hashes omitted.
No money, sequence, status, error, version or row order is discarded.
"""
import argparse
import base64
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import urllib.error
import urllib.parse
import urllib.request
from parity import Capture, canonical


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args):
        return None


class Writes(Capture):
    def __init__(self, *args):
        super().__init__(*args)
        self.ids = {}
        self.opener = urllib.request.build_opener(NoRedirect)

    def register(self, value, label, pattern=r'[0-9A-HJKMNP-TV-Z]{26}'):
        if not re.fullmatch(pattern, value):
            raise ValueError('Unexpected generated identifier format: ' + label)
        self.ids.setdefault(value, '<' + label + '>')

    def normalize(self, value):
        if isinstance(value, dict):
            return {key: self.normalize(item) for key, item in value.items()}
        if isinstance(value, list):
            return [self.normalize(item) for item in value]
        if isinstance(value, str):
            for actual, alias in self.ids.items():
                value = value.replace(actual, alias)
        return value

    def audit_volatility(self, data):
        for event in data.get('audit', []):
            if event['id'] not in self.ids:
                raise ValueError('Refusing to normalize an audit outside the new fixture writes')
            label = self.ids[event['id']].strip('<>')
            self.register(event['hash'], label + '-digest', r'[0-9a-f]{64}')
            self.register(event['prev_hash'], 'audit-parent-of-seq-' + str(event['seq']), r'[0-9a-f]{64}')
            datetime.datetime.fromisoformat(event['at'].replace('Z', '+00:00'))
            event['at'] = '<new-audit-at>'
            outcome = event.get('outcome')
            if not outcome:
                raise ValueError('A completed fixture write is missing its audit outcome')
            self.register(outcome['hash'], label + '-outcome-digest', r'[0-9a-f]{64}')
            datetime.datetime.fromisoformat(outcome['at'].replace('Z', '+00:00'))
            outcome['at'] = '<new-audit-outcome-at>'
        return data

    def write_tool(self, step, name, args, allow_error=False):
        result = self.rpc('tools/call', {'name': name, 'arguments': args})
        envelope = result['body']
        body = envelope.get('result', {})
        if result['status'] != 200 or 'error' in envelope or body.get('isError'):
            if not allow_error:
                self.save(step + '-failure', self.normalize(result))
                raise ValueError('Write fixture failed: ' + step)
        data = body.get('structuredContent', {})
        event = data.get('event_id')
        if event:
            self.register(event, step + '-event')
        for index, event in enumerate(data.get('inverse_event_ids', [])):
            self.register(event, step + '-inverse-' + str(index))
        if name == 'entry_post' and data.get('entry'):
            entry = data['entry']
            self.register(entry['id'], 'entry')
            # Date and contents remain exact; only server creation time varies.
            datetime.datetime.fromisoformat(entry['created_at'].replace('Z', '+00:00'))
            self.ids.setdefault(entry['created_at'], '<new-entry-created-at>')
        if name == 'db_create' and data.get('id'):
            self.register(data['id'], 'table')
        if name == 'import_preview':
            for group in ['proposed', 'duplicates']:
                for index, row in enumerate(data.get(group) or []):
                    self.register(row['id'], 'import-row-' + str(index))
                    if row.get('entry_id'):
                        self.register(row['entry_id'], 'import-entry-' + str(index))
        self.audit_volatility(data)
        # The server repeats structured JSON as text. Preserve both copies.
        for content in body.get('content', []):
            if content.get('type') == 'text':
                try:
                    content['text'] = canonical(self.normalize(self.audit_volatility(json.loads(content['text']))))
                except json.JSONDecodeError:
                    content['text'] = self.normalize(content['text'])
        self.save(step, self.normalize(result))
        return data

    def http(self, step, path, data=None, form=False):
        headers = dict(self.headers)
        if form:
            headers['Content-Type'] = 'application/x-www-form-urlencoded'
            payload = urllib.parse.urlencode(data).encode()
        else:
            payload = None if data is None else json.dumps(data).encode()
        request = urllib.request.Request(self.base + path, data=payload, headers=headers)
        try:
            response = self.opener.open(request, timeout=30)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            raw = response.read()
            try:
                body = json.loads(raw)
            except json.JSONDecodeError:
                body = raw.decode()
            result = {'status': response.status, 'body': body}
            if response.headers.get('Location'):
                result['location'] = response.headers['Location']
            return result

    def run(self):
        for id, type in [('assets:parity-bank', 'asset'), ('expenses:parity-food', 'expense')]:
            self.write_tool('account-' + type, 'account_create',
                            {'id': id, 'type': type, 'name': 'Parity fixture', 'commodities': ['EUR']})
        entry = {'date': '2026-11-01', 'payee': 'Parity first', 'postings': [
            {'account': 'assets:parity-bank', 'amount': {'v': '-10000000000000001.01', 'c': 'EUR'}},
            {'account': 'expenses:parity-food', 'amount': {'v': '10000000000000001.01', 'c': 'EUR'}}]}
        created = self.write_tool('entry-create', 'entry_post', entry)
        assert created['entry']['status'] == 'draft'
        id = created['entry']['id']
        edited = self.write_tool('entry-edit', 'entry_post', dict(entry, id=id, payee='Parity edited'))
        assert edited['entry']['payee'] == 'Parity edited'
        self.write_tool('entry-undo-edit', 'undo', {'event_id': edited['event_id'], 'reason': 'Parity fixture rollback'})
        self.write_tool('entry-delete', 'undo', {'event_id': created['event_id'], 'reason': 'Parity fixture delete draft'})
        deleted = self.write_tool('entry-after-delete', 'entry_get', {'id': id})
        assert deleted['entry']['status'] == 'cancelled' if 'entry' in deleted else deleted['status'] == 'cancelled'
        table = self.write_tool('table-create', 'db_create', {'name': 'Parity unique fixture',
            'columns': [{'id': 'key', 'type': 'string', 'required': True}, {'id': 'value', 'type': 'decimal'}], 'keys': [['key']]})
        db = table['id']
        duplicate = self.write_tool('table-duplicate', 'db_batch', {'ops': [
            {'db': db, 'upsert': {'row_id': 'one', 'cells': {'key': 'same', 'value': '1.50'}}},
            {'db': db, 'upsert': {'row_id': 'two', 'cells': {'key': 'same', 'value': '2.50'}}}]})
        assert duplicate['rows_written'] == 0 and duplicate['violations']
        empty = self.write_tool('table-after-duplicate', 'db_query', {'db': db})
        assert not empty['rows']
        saved = self.write_tool('table-valid', 'db_batch', {'ops': [
            {'db': db, 'upsert': {'row_id': 'one', 'cells': {'key': 'same', 'value': '10000000000000001.01'}}}]})
        assert saved['rows_written'] == 1
        self.write_tool('table-after-valid', 'db_query', {'db': db})
        self.write_tool('table-delete', 'db_drop', {'db': db})
        csv = 'date,amount,currency,payee,id\n2026-11-02,-1.50,EUR,Parity import,parity-duplicate\n2026-11-02,-1.50,EUR,Parity import,parity-duplicate\n'
        args = {'account': 'assets:parity-bank', 'format': 'generic_csv', 'content_b64': base64.b64encode(csv.encode()).decode(),
                'mapping': {'date': 'date', 'amount': 'amount', 'currency': 'currency', 'payee': 'payee', 'id': 'id'}}
        started = self.write_tool('import-start', 'import_start', args)
        preview = self.write_tool('import-preview', 'import_preview', {'batch_id': started['batch_id']})
        assert len(preview['proposed']) == 1, 'duplicate input produced multiple import rows'
        committed = self.write_tool('import-commit', 'import_commit', {'batch_id': started['batch_id']})
        assert committed['posted'] == 1
        repeated = self.write_tool('import-repeat', 'import_start', args)
        assert repeated['batch_id'] == started['batch_id']
        recommitted = self.write_tool('import-recommit', 'import_commit', {'batch_id': repeated['batch_id']})
        assert recommitted['posted'] == 0
        self.write_tool('import-final-preview', 'import_preview', {'batch_id': started['batch_id']})
        self.oauth()
        print('Passed draft create/edit/delete, duplicate-key batch, exact Decimal row, duplicate import and OAuth PKCE/refresh/revoke.')

    def oauth(self):
        redirect = 'http://127.0.0.1:8484/callback'
        registered = self.http('oauth-register', '/oauth/register', {'client_name': 'Parity fixture',
            'redirect_uris': [redirect], 'token_endpoint_auth_method': 'none'})
        assert registered['status'] == 201
        client = registered['body']['client_id']
        self.register(client, 'oauth-client', r'mb_client_[A-Za-z0-9_-]{43}')
        assert isinstance(registered['body']['client_id_issued_at'], int)
        registered['body']['client_id_issued_at'] = '<client-issued-at>'
        self.save('oauth-register', self.normalize(registered))
        verifier = 'parity-verifier-' + 'x' * 48
        challenge = base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).decode().rstrip('=')
        auth = {'client_id': client, 'redirect_uri': redirect, 'response_type': 'code', 'scope': 'ledger:read',
                'state': 'parity-state', 'code_challenge': challenge, 'code_challenge_method': 'S256',
                'resource': self.base + '/mcp', 'decision': 'allow'}
        consent = self.http('oauth-consent', '/oauth/authorize?' + urllib.parse.urlencode(auth))
        assert consent['status'] == 200
        self.save('oauth-consent', self.normalize(consent))
        authorized = self.http('oauth-authorize', '/oauth/authorize', auth, form=True)
        assert authorized['status'] == 303
        params = urllib.parse.parse_qs(urllib.parse.urlsplit(authorized['location']).query)
        assert params['state'] == ['parity-state'] and params['iss'] == [self.base]
        code = params['code'][0]
        self.register(code, 'oauth-code', r'[A-Za-z0-9_-]{43}')
        self.save('oauth-authorize', self.normalize(authorized))
        form = {'client_id': client, 'grant_type': 'authorization_code', 'redirect_uri': redirect,
                'code_verifier': verifier, 'code': code, 'resource': self.base + '/mcp'}
        token = self.http('oauth-token', '/oauth/token', form, form=True)
        assert token['status'] == 200
        for key in ['access_token', 'refresh_token']:
            self.register(token['body'][key], key, r'[A-Za-z0-9_-]{43}')
        self.save('oauth-token', self.normalize(token))
        headers = dict(self.headers)
        headers.pop('Cookie')
        headers['Authorization'] = 'Bearer ' + token['body']['access_token']
        previous = self.headers
        self.headers = headers
        result = self.request('/mcp', {'jsonrpc': '2.0', 'id': 1, 'method': 'tools/call',
                                      'params': {'name': 'accounts_list', 'arguments': {'limit': 1}}})
        assert result['status'] == 200 and not result['body'].get('error')
        self.save('oauth-mcp', self.normalize(result))
        self.headers = previous
        refreshed = self.http('oauth-refresh', '/oauth/token', {'client_id': client, 'grant_type': 'refresh_token',
            'refresh_token': token['body']['refresh_token'], 'resource': self.base + '/mcp'}, form=True)
        assert refreshed['status'] == 200
        for key in ['access_token', 'refresh_token']:
            self.register(refreshed['body'][key], 'refreshed-' + key, r'[A-Za-z0-9_-]{43}')
        self.save('oauth-refresh', self.normalize(refreshed))
        revoked = self.http('oauth-revoke', '/oauth/revoke', {'client_id': client,
            'token': refreshed['body']['refresh_token'], 'token_type_hint': 'refresh_token'}, form=True)
        assert revoked['status'] == 200
        self.save('oauth-revoke', self.normalize(revoked))
        failed = self.http('oauth-after-revoke', '/oauth/token', {'client_id': client, 'grant_type': 'refresh_token',
            'refresh_token': refreshed['body']['refresh_token'], 'resource': self.base + '/mcp'}, form=True)
        assert failed['status'] == 400
        self.save('oauth-after-revoke', self.normalize(failed))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base', required=True)
    parser.add_argument('--session', required=True)
    parser.add_argument('--out', required=True)
    parser.add_argument('--as-of', required=True)
    parser.add_argument('--restored-copy', required=True, action='store_true')
    args = parser.parse_args()
    if urllib.parse.urlsplit(args.base).hostname not in ('localhost', '127.0.0.1'):
        parser.error('Write fixtures require a local disposable restored-copy harness')
    os.umask(0o077)
    Writes(args.base, json.loads(Path(args.session).read_text()), args.out, args.as_of).run()


if __name__ == '__main__':
    main()
