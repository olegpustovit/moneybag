import { useState, type FormEvent } from 'react'
import { useMutation, useQuery, useQueryClient, useSuspenseQuery } from '@tanstack/react-query'
import { createFileRoute, useRouter } from '@tanstack/react-router'
import { Button } from '../components/ui/button'
import { Field, FieldGroup, FieldLabel } from '../components/ui/field'
import { Input } from '../components/ui/input'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '../components/ui/select'
import { ToggleGroup, ToggleGroupItem } from '../components/ui/toggle-group'
import { invokeTool, listTools, settingsPost, tool, type Session, type ToolInfo } from '../lib/api'
import { settingsQuery } from '../lib/queries'

export const Route = createFileRoute('/_app/settings')({
  loader: ({ context }) => context.queryClient.query({ ...settingsQuery(context.session), staleTime: 'static' }),
  pendingComponent: () => <div className="dashboard-pending" aria-busy="true">Loading settings…</div>,
  errorComponent: SettingsError,
  component: Settings,
})
function SettingsError({ reset }: { reset: () => void }) {
  const router = useRouter()
  return <section className="dashboard-error"><h1>Could not load settings</h1><p>Check your connection and try again.</p><Button onClick={() => { reset(); void router.invalidate() }}>Try again</Button></section>
}
function formFields(form: HTMLFormElement): Record<string,string> {
  const fields: Record<string,string> = {}
  for (const [name,value] of new FormData(form)) if (typeof value === 'string') fields[name] = value
  return fields
}
function Message({ error, success }: { error: Error | null; success?: string }) {
  return error ? <p className="form-message error" role="alert">{error.message}</p> : success ? <p className="form-message" role="status">{success}</p> : null
}
function Theme() {
  const [theme, setTheme] = useState(() => { try { const value = localStorage.getItem('moneybag-theme'); return value === 'light' || value === 'dark' ? value : 'system' } catch { return 'system' } })
  return <div className="setting-inline"><div><h2>Appearance</h2><p>Choose a theme or follow your device.</p></div><ToggleGroup aria-label="Appearance" value={[theme]} onValueChange={(values) => { const next = values[0]; if (!next) return; setTheme(next); try { localStorage.setItem('moneybag-theme', next) } catch { /* The choice still applies for this visit. */ } document.documentElement.classList.toggle('dark', next === 'dark' || next === 'system' && matchMedia('(prefers-color-scheme: dark)').matches) }} variant="outline" size="sm" spacing={0}><ToggleGroupItem value="system">System</ToggleGroupItem><ToggleGroupItem value="light">Light</ToggleGroupItem><ToggleGroupItem value="dark">Dark</ToggleGroupItem></ToggleGroup></div>
}
function Settings() {
  const { session } = Route.useRouteContext()
  const { data } = useSuspenseQuery(settingsQuery(session))
  const queryClient = useQueryClient()
  const [confirm, setConfirm] = useState('')
  const [copied, setCopied] = useState(false)
  const [consoleOpen, setConsoleOpen] = useState(false)
  const revoke = useMutation({ mutationFn: (id: string) => settingsPost<{ ok: boolean }>(session, '/settings/revoke', { id }), onSuccess: async () => { setConfirm(''); await queryClient.invalidateQueries({ queryKey: settingsQuery(session).queryKey }) } })
  const sync = useMutation({ mutationFn: (feed: string) => tool(session, 'feeds_sync', { feed }), onSuccess: (result) => { window.location.assign(`/imports/${encodeURIComponent(result.batch_id)}`) } })
  const agents = data.agents ?? []
  const feeds = data.feeds ?? []
  return <><header className="page-header"><div><p className="breadcrumb">WORKSPACE / SETTINGS</p><h1>Settings</h1><p className="description">Your account and connected agents.</p></div></header>
    <div className="settings-layout">
      <section className="settings-section"><Theme /></section>
      <section className="settings-section"><h2>Connected agents</h2><p>Agents can work within the scopes you granted.</p>
        {agents.length === 0 ? <p className="settings-empty">No agents connected yet.</p> : <div className="table-scroll" tabIndex={0} role="region" aria-label="Connected agents"><table><thead><tr><th>Agent</th><th>Access</th><th>Status</th><th><span className="sr-only">Action</span></th></tr></thead><tbody>{agents.map((agent) => <tr key={agent.id}><td>{agent.client_name || agent.id}</td><td>{(agent.scopes ?? []).join(', ') || '—'}</td><td>{agent.revoked ? 'Revoked' : 'Connected'}</td><td>{!agent.revoked && (confirm === agent.id ? <span className="setting-actions"><Button variant="destructive" size="xs" disabled={revoke.isPending} onClick={() => revoke.mutate(agent.id)}>Confirm revoke</Button><Button variant="ghost" size="xs" onClick={() => setConfirm('')}>Cancel</Button></span> : <Button variant="outline" size="xs" onClick={() => setConfirm(agent.id)}>Revoke</Button>)}</td></tr>)}</tbody></table></div>}
        <Message error={revoke.error}/>
        <div className="endpoint"><div><span className="setting-label">MCP endpoint</span><code>{new URL('/mcp', window.location.origin).href}</code></div><Button variant="outline" size="sm" onClick={async () => { try { await navigator.clipboard.writeText(new URL('/mcp', window.location.origin).href); setCopied(true) } catch { setCopied(false) } }}>{copied ? 'Copied' : 'Copy'}</Button></div>
      </section>
      <section className="settings-section"><h2>Statement feeds</h2><p>Connect a feed to stage statements for review.</p>
        {feeds.length === 0 ? <p className="settings-empty">No feeds connected yet.</p> : <div className="table-scroll" tabIndex={0} role="region" aria-label="Statement feeds"><table><thead><tr><th>Feed</th><th>Account</th><th>Format</th><th><span className="sr-only">Action</span></th></tr></thead><tbody>{feeds.map((feed) => <tr key={feed.id}><td>{feed.id}</td><td>{feed.account}</td><td>{feed.format}</td><td><Button variant="outline" size="xs" disabled={sync.isPending} onClick={() => sync.mutate(feed.id)}>Sync</Button></td></tr>)}</tbody></table></div>}
        <Message error={sync.error}/><FeedForm session={session}/>
      </section>
      {session.user.is_admin && <section className="settings-section"><InviteForm session={session}/></section>}
      <section className="settings-section"><h2>Account</h2><p>{session.user.name} · {session.user.email}</p><PasswordForm session={session}/><form action="/logout" method="post"><input type="hidden" name="csrf" value={session.csrf}/><Button type="submit" variant="outline" size="sm">Sign out</Button></form></section>
      <section className="settings-section"><details onToggle={(event) => setConsoleOpen(event.currentTarget.open)}><summary>MCP console <span>Run a tool against your workspace</span></summary>{consoleOpen && <ToolConsole session={session}/>}</details></section>
    </div>
  </>
}
function InviteForm({ session }: { session: Session }) {
  const invite = useMutation({ mutationFn: (fields: Record<string,string>) => settingsPost<{ invitation_url: string }>(session, '/settings/invite', fields) })
  const [copied, setCopied] = useState(false)
  return <><h2>Invite a user</h2><p>Each user has private records. Share dashboards explicitly.</p><form className="settings-form" onSubmit={(event) => { event.preventDefault(); invite.mutate(formFields(event.currentTarget)) }}><FieldGroup><Field><FieldLabel htmlFor="invite-email">Email</FieldLabel><Input id="invite-email" name="email" type="email" autoComplete="email" required/></Field></FieldGroup><Button type="submit" size="sm" disabled={invite.isPending}>Create invitation</Button></form><Message error={invite.error}/>{invite.data && <div className="invitation"><label htmlFor="invitation-url">Invitation link</label><Input id="invitation-url" readOnly value={invite.data.invitation_url}/><Button variant="outline" size="sm" onClick={async () => { try { await navigator.clipboard.writeText(invite.data.invitation_url); setCopied(true) } catch { setCopied(false) } }}>{copied ? 'Copied' : 'Copy link'}</Button></div>}</>
}
function FeedForm({ session }: { session: Session }) {
  const queryClient = useQueryClient()
  const [feed, setFeed] = useState('wise')
  const [format, setFormat] = useState('wise_csv')
  const [dateFormat, setDateFormat] = useState('auto')
  const [saved, setSaved] = useState(false)
  const save = useMutation({ mutationFn: (fields: Record<string,string>) => settingsPost<{ok:boolean}>(session, '/settings/feed', fields), onSuccess: async () => { await queryClient.invalidateQueries({queryKey:settingsQuery(session).queryKey}) } })
  const submit = (event: FormEvent<HTMLFormElement>) => { event.preventDefault(); const form = event.currentTarget; setSaved(false); save.mutate({ ...formFields(form), id:feed, format, date_format:dateFormat === 'auto' ? '' : dateFormat }, { onSuccess: () => { form.reset(); save.reset(); setSaved(true) } }) }
  return <details className="setting-details"><summary>Connect or update a feed</summary><form className="settings-form vertical" onSubmit={submit}><FieldGroup>
    <Field><FieldLabel htmlFor="feed-kind">Feed</FieldLabel><Select value={feed} onValueChange={(v) => { if(v) setFeed(v) }}><SelectTrigger id="feed-kind"><SelectValue /></SelectTrigger><SelectContent><SelectGroup><SelectItem value="wise">Wise</SelectItem><SelectItem value="finance_connector">Finance connector</SelectItem></SelectGroup></SelectContent></Select></Field>
    <Field><FieldLabel htmlFor="feed-url">Public HTTPS feed URL</FieldLabel><Input id="feed-url" name="url" type="url" required placeholder="https://example.com/statement" /></Field>
    <Field><FieldLabel htmlFor="feed-account">Account ID</FieldLabel><Input id="feed-account" name="account" required placeholder="assets:bank" /></Field>
    <Field><FieldLabel htmlFor="feed-format">Format</FieldLabel><Select value={format} onValueChange={(v) => { if(v) setFormat(v) }}><SelectTrigger id="feed-format"><SelectValue /></SelectTrigger><SelectContent><SelectGroup><SelectItem value="wise_csv">Wise CSV</SelectItem><SelectItem value="generic_csv">Generic CSV</SelectItem><SelectItem value="plaid">Plaid</SelectItem></SelectGroup></SelectContent></Select></Field>
    <Field><FieldLabel htmlFor="date-format">Date format</FieldLabel><Select value={dateFormat} onValueChange={(v) => setDateFormat(v ?? 'auto')}><SelectTrigger id="date-format"><SelectValue placeholder="Automatic" /></SelectTrigger><SelectContent><SelectGroup><SelectItem value="auto">Automatic</SelectItem><SelectItem value="iso">Year-month-day</SelectItem><SelectItem value="dmy">Day-month-year</SelectItem><SelectItem value="mdy">Month-day-year</SelectItem></SelectGroup></SelectContent></Select></Field>
    <Field><FieldLabel htmlFor="feed-token">Access token <span className="muted">(optional)</span></FieldLabel><Input id="feed-token" name="token" type="password" autoComplete="new-password" /></Field>
  </FieldGroup><Button type="submit" size="sm" disabled={save.isPending}>Save feed</Button><Message error={save.error} success={saved ? 'Feed saved.' : undefined}/></form></details>
}
function PasswordForm({ session }: { session: Session }) {
  const change = useMutation({ mutationFn: (fields: Record<string,string>) => settingsPost<{ok:boolean;redirect:string}>(session, '/settings/password', fields), onSuccess: (result) => { window.location.assign(result.redirect) } })
  return <details className="setting-details"><summary>Change password</summary><form className="settings-form vertical" onSubmit={(event) => { event.preventDefault(); change.mutate(formFields(event.currentTarget)) }}><FieldGroup><Field><FieldLabel htmlFor="current-password">Current password</FieldLabel><Input id="current-password" name="current" type="password" autoComplete="current-password" required/></Field><Field><FieldLabel htmlFor="new-password">New password</FieldLabel><Input id="new-password" name="next" type="password" autoComplete="new-password" minLength={12} maxLength={256} required/></Field></FieldGroup><Button type="submit" size="sm" disabled={change.isPending}>Change password and sign out</Button><Message error={change.error}/></form></details>
}
function ToolConsole({ session }: { session: Session }) {
  const catalog = useQuery({ queryKey: ['tool-catalog', session.user.id], queryFn: () => listTools(session) })
  const [name, setName] = useState('')
  const [parseError, setParseError] = useState('')
  const run = useMutation({ mutationFn: (args: Record<string, unknown>) => invokeTool(session, name, args) })
  const selected: ToolInfo | undefined = catalog.data?.find((item) => item.name === name)
  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    const args: Record<string, unknown> = {}
    try {
      for (const [key, schema] of Object.entries(selected?.inputSchema.properties ?? {})) {
        const value = data.get(key)
        if (value === null || value === '') continue
        if (schema.type === 'string') args[key] = value
        else args[key] = JSON.parse(String(value)) as unknown
      }
      setParseError('')
      run.mutate(args)
    } catch { setParseError('Enter valid JSON for non-text values.') }
  }
  return <div className="settings-console"><p>Choose a tool and provide its arguments. Operations run with your account permissions.</p>{catalog.isLoading ? <p>Loading tools…</p> : catalog.error ? <Message error={catalog.error}/> : <><Field><FieldLabel htmlFor="tool-name">Tool</FieldLabel><Select value={name} onValueChange={(value) => { setName(value ?? ''); setParseError(''); run.reset() }}><SelectTrigger id="tool-name"><SelectValue placeholder="Choose a tool" /></SelectTrigger><SelectContent><SelectGroup>{catalog.data?.map((item) => <SelectItem key={item.name} value={item.name}>{item.name}</SelectItem>)}</SelectGroup></SelectContent></Select></Field>{selected && <form key={name} className="settings-form vertical" onSubmit={submit}><p>{selected.description}</p><FieldGroup>{Object.entries(selected.inputSchema.properties ?? {}).map(([key, schema]) => <Field key={key}><FieldLabel htmlFor={`tool-${key}`}>{key}{selected.inputSchema.required?.includes(key) ? '' : ' (optional)'}</FieldLabel><Input id={`tool-${key}`} name={key} required={selected.inputSchema.required?.includes(key)} placeholder={schema.type === 'string' ? undefined : 'JSON value'} /><p>{schema.description}</p></Field>)}</FieldGroup><Button type="submit" size="sm" disabled={run.isPending}>Run tool</Button>{parseError && <p role="alert">{parseError}</p>}<Message error={run.error}/>{run.isSuccess && <pre className="console-result">{JSON.stringify(run.data, null, 2)}</pre>}</form>}</>}</div>
}
