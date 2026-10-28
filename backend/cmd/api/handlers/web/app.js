(() => {
  'use strict';
  const $ = s => document.querySelector(s);
  if (!$('meta[name="csrf-token"]')) return;
  const csrf = $('meta[name="csrf-token"]').content;
  let requestID = 0, ready, protocol = '2026-07-28';
  async function rpc(method, params) {
    params = { ...params, _meta: { 'io.modelcontextprotocol/protocolVersion': protocol, 'io.modelcontextprotocol/clientInfo': { name: 'moneybag-web', version: '1' }, 'io.modelcontextprotocol/clientCapabilities': {} } };
    const headers = { 'Content-Type': 'application/json', 'Accept': 'application/json, text/event-stream', 'X-CSRF-Token': csrf, 'MCP-Protocol-Version': protocol, 'Mcp-Method': method };
    if (params.name) headers['Mcp-Name'] = params.name;
    const response = await fetch('/api/mcp', { method: 'POST', credentials: 'same-origin', headers, body: JSON.stringify({ jsonrpc: '2.0', id: ++requestID, method, params }) });
    if (!response.ok) throw Error(response.status === 401 ? 'Your session expired. Sign in again.' : `Request failed (${response.status}). Reload and retry.`);
    const envelope = await response.json();
    if (envelope.error) throw Error(envelope.error.message);
    return envelope.result;
  }
  async function connect() {
    if (!ready) ready = rpc('server/discover', {}).then(result => { if (!result.supportedVersions?.includes(protocol)) throw Error('This client and server need matching protocol versions. Reload the page.'); }).catch(error => { ready = null; throw error; });
    return ready;
  }
  async function tool(name, args = {}) {
    await connect();
    const result = await rpc('tools/call', { name, arguments: args });
    if (result.isError) throw Error(result.content?.filter(c => c.type === 'text').map(c => c.text).join('\n') || 'The operation failed. Review the input and try again.');
    if (result.structuredContent !== undefined) return result.structuredContent;
    const text = result.content?.find(c => c.type === 'text')?.text;
    try { return JSON.parse(text); } catch { return text; }
  }
  function status(message, error = false) {
    const el = $('#status'); if (!el) return;
    el.textContent = message; el.classList.toggle('error', error); el.hidden = false;
  }
  const main = $('#main');
  async function runButton(button, action) {
    button.disabled = true; const original = button.textContent; button.textContent = 'Working…';
    try { await action(); } catch (error) { status(error.message, true); } finally { button.disabled = false; button.textContent = original; }
  }
  document.addEventListener('click', event => {
    const close = event.target.closest('[data-close]'); if (close) { close.closest('dialog').close(); return; }
    const button = event.target.closest('[data-action]'); if (!button) return;
    const action = button.dataset.action;
    if (action === 'new-dashboard') return $('#create-dashboard').showModal();
    if (action === 'new-table') return $('#create-table').showModal();
    if (action === 'share') return $('#share-dashboard').showModal();
    if (action === 'new-entry') return $('#create-entry').showModal();
    if (action === 'spec') { const el = $('#spec-drawer'); el.hidden = !el.hidden; if (!el.hidden) $('#spec-input').focus(); return; }
    runButton(button, async () => {
      const id = main.dataset.dashboard;
	  if (action === 'unshare') { await tool('dashboard_unshare', {share_id: button.dataset.id}); location.reload(); }
      if (action === 'publish') { await tool('dashboard_publish', { dashboard_id: id, version: Number(main.dataset.version) }); location.href = `${main.dataset.dashboardPath}?version=published`; }
      if (action === 'save-spec') {
        const spec = JSON.parse($('#spec-input').value);
        const result = await tool('dashboard_patch', { dashboard_id: id, ops: [{ op: 'replace', path: '', value: spec }] });
        if (result.validation && !result.validation.ok) status(`Saved draft v${result.draft_version}. Fix its validation errors before publishing.`, true);
        location.href = `${main.dataset.dashboardPath}?version=draft`;
      }
      if (action === 'validate') { const result = await tool('dashboard_validate', { dashboard_id: id, version: Number(main.dataset.version) }); const out = $('#validation-result'); out.textContent = JSON.stringify(result, null, 2); out.hidden = false; status(result.ok ? 'Validation passed.' : 'Correct the listed errors before publishing.', !result.ok); }
      if (action === 'run-console') {
        const input = JSON.parse($('#console-input').value);
        if (typeof input.tool !== 'string') throw Error('Enter {"tool":"tool_name","arguments":{}}.');
        try { const result = await tool(input.tool, input.arguments || {}); $('#console-output').textContent = JSON.stringify(result, null, 2); status(`${input.tool} completed. Reload the page to see updated views.`); } catch (error) { $('#console-output').textContent = error.message; throw error; }
      }
      if (action === 'list-tools') { await connect(); const tools = []; let cursor; do { const result = await rpc('tools/list', cursor ? { cursor } : {}); tools.push(...result.tools); cursor = result.nextCursor; } while (cursor); const select = $('#console-tools'); select.replaceChildren(...tools.map(t => { const option = document.createElement('option'); option.value = t.name; option.textContent = t.name; option.title = t.description; return option; })); status(`${tools.length} tools available.`); }
      if (action === 'reconcile-suggest') { const result=await tool('reconcile_suggest',{discrepancy_id:button.dataset.id});const out=button.nextElementSibling;out.textContent=JSON.stringify(result.candidates,null,2);out.hidden=false;}
      if (action === 'add-row') {
        const rowID = 'row_' + crypto.randomUUID();
        const result = await tool('db_list'); const table = result.items.find(t => t.id === main.dataset.table);
        if (!table) throw Error('Database is unavailable. Reload the page.');
        const column = table.columns.find(c => !c.formula && c.type !== 'formula'); if (!column) throw Error('Add an editable column before creating rows.');
        const updates=table.columns.filter(c=>!c.formula&&c.type!=='formula').map(c=>({row_id:rowID,column:c.id,value:c.type==='number'?0:c.type==='bool'?false:c.type==='date'?new Date().toISOString().slice(0,10):''}));
        await tool('db_update_cells', { db: table.id, updates }); location.reload();
      }
    });
  });
  $('#console-tools')?.addEventListener('change', event => { $('#console-input').value = JSON.stringify({ tool: event.target.value, arguments: {} }, null, 2); });
  function form(selector, callback) {
    (typeof selector === 'string' ? $(selector) : selector)?.addEventListener('submit', async event => {
      event.preventDefault(); const f = event.currentTarget, button = f.querySelector('button'), error = f.querySelector('.form-error'); button.disabled = true; if (error) error.hidden = true;
      try { await callback(new FormData(f)); } catch (e) { if (error) { error.textContent = e.message; error.hidden = false; } else status(e.message, true); } finally { button.disabled = false; }
    });
  }
  form('#dashboard-form', async data => { const id = data.get('id'); await tool('dashboard_create', { id, title: data.get('title'), from_template: data.get('template') }); location.href = `/dashboards/${encodeURIComponent(id)}`; });
  form('#table-form', async data => { const table = await tool('db_create', { name: data.get('name'), columns: JSON.parse(data.get('columns')) }); location.href = `/databases/${encodeURIComponent(table.id)}`; });
  form('#entry-form', async data => {
    const amount = data.get('amount').trim(); if (!/^[0-9]+(\.[0-9]+)?$/.test(amount) || Number(amount) <= 0) throw Error('Enter a positive decimal amount.');
    const commodity = data.get('commodity'); await tool('entry_post', { date: data.get('date'), payee: data.get('payee'), commit: data.get('commit') === 'on', postings: [{ account: data.get('debit'), amount: { v: amount, c: commodity } }, { account: data.get('credit'), amount: { v: '-' + amount, c: commodity } }] }); location.reload();
  });
  form('#rollback-form', async data => { await tool('dashboard_rollback', { dashboard_id: main.dataset.dashboard, to_version: Number(data.get('version')) }); location.href = `${main.dataset.dashboardPath}?version=published`; });

  document.querySelectorAll('form[data-tool]').forEach(f=>form(f,async data=>{
    const args={};
    f.querySelectorAll('[name]').forEach(input=>{
      if(input.disabled)return;
      let value=data.get(input.name);
      if(input.dataset.kind==='bool')value=input.type==='checkbox'?input.checked:value==='true';
      else if(input.dataset.kind==='list')value=String(value||'').split(',').map(v=>v.trim()).filter(Boolean);
      else if(input.dataset.kind==='number'){value=Number(value);if(!Number.isFinite(value))throw Error('Enter a finite number.');}
      const keys=input.name.split('.');let target=args;keys.slice(0,-1).forEach(k=>target=target[k]||=( {} ));target[keys.at(-1)]=value;
    });
    const result=await tool(f.dataset.tool,args);
    if(f.dataset.tool==='feeds_sync')location.href='/imports/'+encodeURIComponent(result.batch_id);else location.reload();
  }));
  form('#import-form',async data=>{
    const file=data.get('file');if(!file||file.size===0||file.size>8*1024*1024)throw Error('Choose a statement file up to 8 MiB.');
    const bytes=new Uint8Array(await file.arrayBuffer());let binary='';for(let i=0;i<bytes.length;i+=16384)binary+=String.fromCharCode(...bytes.subarray(i,i+16384));
    const result=await tool('import_start',{account:data.get('account'),format:data.get('format'),content_b64:btoa(binary)});location.href='/imports/'+encodeURIComponent(result.batch_id);
  });
  form('#scenario-form',async data=>{
    const overrides=[];for(const assumption of ['income_usd','spend_eur']){const raw=data.get(assumption);if(raw!==''){const value=Number(raw);if(!Number.isFinite(value))throw Error('Overrides must be finite numbers.');overrides.push({assumption,value});}}
    await tool('scenario_upsert',{id:data.get('id'),name:data.get('name'),overrides});location.reload();
  });
  form('#share-form',async data=>{await tool('dashboard_share',{dashboard_id:main.dataset.dashboard,grantee:data.get('grantee'),role:data.get('role'),mode:data.get('mode'),sources:String(data.get('sources')).split(',').map(s=>s.trim()).filter(Boolean),row_filter:data.get('row_filter')});location.reload();});
  let saveQueue = Promise.resolve();
  function cellText(td, value) { const div = document.createElement('div'); div.textContent = value; td.replaceChildren(div); }
  document.querySelectorAll('.grid-ed td').forEach(td => {
    td.addEventListener('focus', () => { if ($('#formula-display')) $('#formula-display').value = td.dataset.formula || td.textContent.trim(); if (td.dataset.edit) td.dataset.original = td.textContent.trim(); });
    if (!td.dataset.edit) return;
    td.addEventListener('keydown', event => { if (event.key === 'Enter') { event.preventDefault(); td.blur(); } if (event.key === 'Escape') { event.preventDefault(); cellText(td, td.dataset.original); td.blur(); } });
    td.addEventListener('blur', () => {
      const value = td.textContent.trim(), original = td.dataset.original; cellText(td, value); if (value === original) return;
      td.dataset.saving = 'true'; td.setAttribute('aria-busy', 'true');
      saveQueue = saveQueue.catch(() => {}).then(async () => {
        try {
          const row = td.closest('tr'), column = td.dataset.edit;
          if (main.dataset.gridTool === 'budget_set_line') {
            if (column === 'limit' && !/^[0-9]+(\.[0-9]+)?$/.test(value)) throw Error('Budget limits must be nonnegative decimal amounts.');
            await tool('budget_set_line', { id: row.dataset.row, [column]: value });
          } else {
            let typed = value; if (td.dataset.type === 'number') { typed = Number(value); if (value === '' || !Number.isFinite(typed)) throw Error('Enter a finite number.'); }
            if (td.dataset.type === 'bool') { if (!['true', 'false'].includes(value)) throw Error('Enter true or false.'); typed = value === 'true'; }
            const result = await tool('db_update_cells', { db: main.dataset.table, updates: [{ row_id: row.dataset.row, column, value: typed, version: Number(row.dataset.revision) }] });
            const updated = result.rows.find(r => r.id === row.dataset.row); if (updated) { row.dataset.revision = updated.version; row.querySelectorAll('[data-formula]').forEach(cell => { const key = cell.dataset.edit; if (!key) { const index = Array.from(row.cells).indexOf(cell); const header = document.querySelectorAll('.grid-ed th')[index]; if (cell.dataset.formula && header) { const name = header.textContent.replace('ƒ', '').trim(); if (updated.cells[name] !== undefined) cellText(cell, String(updated.cells[name])); } } }); }
          }
          td.classList.add('flash'); td.removeAttribute('aria-invalid'); status('Saved. Dashboards will use the updated value.');
        } catch (error) { cellText(td, original); td.setAttribute('aria-invalid', 'true'); status(error.message, true); }
        finally { delete td.dataset.saving; td.removeAttribute('aria-busy'); }
      });
    });
  });
})();
