import json
import os
import subprocess
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import runpy
import tempfile
import unittest
from pathlib import Path
from parity import Capture, canonical


class ParityTest(unittest.TestCase):
    def test_preserves_data_and_only_omits_render_timings(self):
        with tempfile.TemporaryDirectory() as directory:
            capture = Capture('http://localhost', {'cookie': 'secret', 'csrf': 'csrf'},
                              Path(directory) / 'out', '2026-11-14')
            capture.readonly = {'dashboard_render'}
            value = {'timing_ms': 123, 'render_ms': 456, 'amount': '-0.00',
                     'version': 2, 'rows': [2, 1], 'generated_at': 'kept'}
            capture.rpc = lambda *args: {'status': 200, 'body': {'result': {
                'structuredContent': dict(value),
                'content': [{'type': 'text', 'text': json.dumps(value)}]}}}
            result = capture.tool('dashboard_render', {'id': 'dashboard'})
            self.assertEqual(result, {k: v for k, v in value.items()
                                      if k not in ('timing_ms', 'render_ms')})
            saved = json.loads((capture.out / 'tool-dashboard_render.json').read_text())
            self.assertEqual(json.loads(saved['body']['result']['content'][0]['text']), result)
            with self.assertRaisesRegex(ValueError, 'not read-only'):
                capture.tool('entry_create', {})

    def test_generated_write_ids_keep_references_and_preserve_other_values(self):
        writes = runpy.run_path(str(Path(__file__).with_name('parity-writes.py')))['Writes']
        with tempfile.TemporaryDirectory() as directory:
            capture = writes('http://localhost', {'cookie': 'secret', 'csrf': 'csrf'},
                             Path(directory) / 'out', '2026-11-14')
            id = '01ARZ3NDEKTSV4RRFFQ69G5FAV'
            capture.register(id, 'created-entry')
            capture.register(id, 'same-reference')
            value = {'id': id, 'link': id, 'amount': '99999999999999999.01',
                     'unknown': '01ARZ3NDEKTSV4RRFFQ69G5FAW', 'seq': 42}
            result = capture.normalize(value)
            self.assertEqual(result['id'], '<created-entry>')
            self.assertEqual(result['link'], result['id'])
            for key in ['amount', 'unknown', 'seq']:
                self.assertEqual(result[key], value[key])
            with self.assertRaisesRegex(ValueError, 'format'):
                capture.register('invalid', 'bad-id')
            with self.assertRaisesRegex(ValueError, 'outside'):
                capture.audit_volatility({'audit': [{'id': value['unknown']}]})

    @unittest.skipUnless(os.environ.get('MONEYBAG_CHROMIUM_BIN'), 'set MONEYBAG_CHROMIUM_BIN for real shared-DOM capture')
    def test_shared_dom_renders_and_blocks_writes(self):
        requests = []

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *args):
                pass

            def do_GET(self):
                requests.append(('GET', self.path, self.headers.get('Cookie')))
                self.send_response(200)
                self.end_headers()
                if self.path.startswith('/api/shared/'):
                    self.wfile.write(b'{"title":"Rendered shared fixture"}')
                else:
                    self.wfile.write(b'''<html><body><h1 id="shared">Loading</h1><script>
                    fetch('/api/shared/fixture').then(r=>r.json()).then(d=>document.querySelector('#shared').textContent=d.title);
                    fetch('/api/usage',{method:'POST'}).catch(()=>{});
                    for (const name of ['accounts_list','entry_post']) fetch('/api/mcp',{method:'POST',body:JSON.stringify({method:'tools/call',params:{name}})}).catch(()=>{});
                    </script></body></html>''')

            def do_POST(self):
                name = self.path
                if self.path == '/api/mcp':
                    body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
                    name = body['params']['name']
                requests.append(('POST', name, self.headers.get('Cookie')))
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b'{}')

        server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            result = subprocess.run(['node', str(Path(__file__).with_name('parity-shared.mjs'))],
                                    input=json.dumps({'base': 'http://127.0.0.1:' + str(server.server_port),
                                                      'cookie': 'synthetic-cookie', 'id': 'fixture',
                                                      'readonly': ['accounts_list']}),
                                    text=True, capture_output=True, timeout=30, check=True)
            self.assertIn('<h1 id="shared">Rendered shared fixture</h1>', json.loads(result.stdout)['html'])
            self.assertEqual([r[1] for r in requests if r[0] == 'POST'], ['accounts_list'])
            self.assertTrue(all('moneybag_session=synthetic-cookie' in r[2] for r in requests))
        finally:
            server.shutdown()
            server.server_close()
            thread.join()

    def test_canonical_json_keeps_array_order_and_exact_money_strings(self):
        self.assertEqual(canonical({'b': ['999999999999999999.01', '-0.00'], 'a': 2}),
                         canonical({'a': 2, 'b': ['999999999999999999.01', '-0.00']}))
        self.assertNotEqual(canonical([2, 1]), canonical([1, 2]))


if __name__ == '__main__':
    unittest.main()
