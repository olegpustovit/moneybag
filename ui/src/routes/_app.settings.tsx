import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { RouteError } from '../features/telemetry/route-error'
import { createFileRoute } from '@tanstack/react-router'
import { Button } from '../components/ui/button'
import { Field, FieldGroup, FieldLabel } from '../components/ui/field'
import { Input } from '../components/ui/input'
import { ToggleGroup, ToggleGroupItem } from '../components/ui/toggle-group'
import { deleteView, savedViewsQuery, settingsPost, settingsQuery, type Session } from '../lib/api'
import { clearCached } from '../features/telemetry/cube-store'
import { describePage, searchKey } from '../features/telemetry/shell-store'
import { RowsSkeleton, humanKey } from '../features/telemetry/page-bits'

export const Route=createFileRoute('/_app/settings')({errorComponent:p => <RouteError {...p}/>,component:Settings})
const fieldsOf=(form:HTMLFormElement) => {const out:Record<string,string>={};for(const [k,v] of new FormData(form))if(typeof v==='string')out[k]=v;return out}
function Message({error,success}:{error:Error|null;success?:string}) {return error ? <p className="form-message error" role="alert">{error.message}</p> : success ? <p className="form-message" role="status">{success}</p> : null}
function Theme() {
 const [theme,setTheme]=useState(() => {try {const v=localStorage.getItem('moneybag-theme');return v==='light' || v==='dark' ? v : 'system'} catch {return 'system'}})
 return <div className="setting-inline"><div><h2>Appearance</h2><p>Choose a theme or follow your device.</p></div><ToggleGroup aria-label="Appearance" value={[theme]} onValueChange={values => {const next=values[0];if(!next)return;setTheme(next);try {localStorage.setItem('moneybag-theme',next)} catch {/* The choice still applies for this visit. */}document.documentElement.classList.toggle('dark',next==='dark' || (next==='system' && matchMedia('(prefers-color-scheme: dark)').matches))}} variant="outline" size="sm" spacing={0}><ToggleGroupItem value="system">System</ToggleGroupItem><ToggleGroupItem value="light">Light</ToggleGroupItem><ToggleGroupItem value="dark">Dark</ToggleGroupItem></ToggleGroup></div>
}
const ACCESS:Record<string,string>={'ledger:read':'Read the ledger','ledger:write':'Post entries','dash:read':'Read dashboards','dash:write':'Build dashboards','import:write':'Import statements','model:write':'Change plans','admin:users':'Manage people'}
function Agents({session,data}:{session:Session;data:NonNullable<ReturnType<typeof useQuery<import('../lib/api').SettingsData>>['data']>}) {
 const client=useQueryClient(),[confirm,setConfirm]=useState(''),[copied,setCopied]=useState(false),endpoint=new URL('/mcp',location.origin).href,agents=(data.agents ?? []).filter(a => !a.revoked)
 const revoke=useMutation({mutationFn:(id:string) => settingsPost<{ok:boolean}>(session,'/settings/revoke',{id}),onSuccess:async () => {setConfirm('');await client.invalidateQueries({queryKey:settingsQuery(session).queryKey})}})
 const toggle=useMutation({mutationFn:(stop:boolean) => settingsPost<{agents_suspended:boolean}>(session,stop ? '/settings/agents/stop' : '/settings/agents/resume',{}),onSuccess:async () => {setConfirm('');await client.invalidateQueries({queryKey:settingsQuery(session).queryKey})}})
 return <section className="settings-section"><h2>Connected agents</h2><p>Agents build and run everything here, within the access you granted.</p>
  {agents.length ? <ul className="tm-list static">{agents.map(a => <li key={a.id}><div><span className="n">{a.client_name || 'An agent'}<small>{(a.scopes ?? []).map(s => ACCESS[s] ?? humanKey(s.replace(':',' '))).join(' · ') || 'No access'}</small></span>{confirm===a.id ? <span className="tm-row"><Button size="sm" variant="destructive" disabled={revoke.isPending} onClick={() => revoke.mutate(a.id)}>Disconnect</Button><Button size="sm" variant="outline" onClick={() => setConfirm('')}>Cancel</Button></span> : <Button size="sm" variant="outline" onClick={() => setConfirm(a.id)}>Disconnect</Button>}</div></li>)}</ul> : <p className="settings-empty">No agents connected yet. Add this address to one to get started.</p>}
  <Message error={revoke.error ?? toggle.error}/>
  <div className="endpoint"><div><span className="setting-label">Address for agents</span><code>{endpoint}</code></div><Button variant="outline" size="sm" onClick={async () => {try {await navigator.clipboard.writeText(endpoint);setCopied(true)} catch {setCopied(false)}}}>{copied ? 'Copied' : 'Copy'}</Button></div>
  {data.is_admin!==false && <div className={`tm-stop${data.agents_suspended ? ' on' : ''}`}><div><strong>{data.agents_suspended ? 'All agents are stopped' : 'Emergency stop'}</strong><p>{data.agents_suspended ? 'No agent can read or change anything until you resume them.' : 'Suspends every agent at once, until you resume them.'}</p></div>{data.agents_suspended ? <Button size="sm" disabled={toggle.isPending} onClick={() => toggle.mutate(false)}>Resume agents</Button> : confirm==='stop' ? <span className="tm-row"><Button size="sm" variant="destructive" disabled={toggle.isPending} onClick={() => toggle.mutate(true)}>Stop all agents</Button><Button size="sm" variant="outline" onClick={() => setConfirm('')}>Cancel</Button></span> : <Button size="sm" variant="destructive" onClick={() => setConfirm('stop')}>Stop all agents</Button>}</div>}
 </section>
}
function Feeds({data}:{data:{feeds:{id:string;account:string;format:string}[]|null}}) {
 const feeds=data.feeds ?? []
 return <section className="settings-section"><h2>Statement feeds</h2><p>Agents set feeds up. The bank’s secret only ever goes in here, on its own secure page.</p>{feeds.length ? <ul className="tm-list static">{feeds.map(f => <li key={f.id}><div><span className="n">{humanKey(f.id)}<small>{humanKey(f.format)}</small></span><a className="tm-btn small" href={`/secure/feed/${encodeURIComponent(f.id)}`}>Enter or replace secret</a></div></li>)}</ul> : <p className="settings-empty">No feeds yet. Ask an agent to connect your bank.</p>}</section>
}
function Invite({session}:{session:Session}) {
 const invite=useMutation({mutationFn:(fields:Record<string,string>) => settingsPost<{invitation_url:string}>(session,'/settings/invite',fields)}),[copied,setCopied]=useState(false)
 return <section className="settings-section"><h2>Invite someone</h2><p>Each person has private records. Sharing a dashboard is always explicit.</p><form className="settings-form" onSubmit={e => {e.preventDefault();invite.mutate(fieldsOf(e.currentTarget))}}><FieldGroup><Field><FieldLabel htmlFor="invite-email">Email</FieldLabel><Input id="invite-email" name="email" type="email" autoComplete="email" required/></Field></FieldGroup><Button type="submit" size="sm" disabled={invite.isPending}>Create invitation</Button></form><Message error={invite.error}/>{invite.data && <div className="invitation"><Input readOnly aria-label="Invitation link" value={invite.data.invitation_url}/><Button variant="outline" size="sm" onClick={async () => {try {await navigator.clipboard.writeText(invite.data.invitation_url);setCopied(true)} catch {setCopied(false)}}}>{copied ? 'Copied' : 'Copy'}</Button></div>}</section>
}
function Account({session}:{session:Session}) {
 const change=useMutation({mutationFn:(fields:Record<string,string>) => settingsPost<{ok:boolean;redirect:string}>(session,'/settings/password',fields),onSuccess:async result => {await clearCached();location.assign(result.redirect)}})
 return <section className="settings-section"><h2>Account</h2><p>{session.user.name} · {session.user.email}</p>
  <details className="setting-details"><summary>Change password</summary><form className="settings-form vertical" onSubmit={e => {e.preventDefault();change.mutate(fieldsOf(e.currentTarget))}}><FieldGroup><Field><FieldLabel htmlFor="current-password">Current password</FieldLabel><Input id="current-password" name="current" type="password" autoComplete="current-password" required/></Field><Field><FieldLabel htmlFor="new-password">New password</FieldLabel><Input id="new-password" name="next" type="password" autoComplete="new-password" required minLength={8}/></Field></FieldGroup><Button type="submit" size="sm" disabled={change.isPending}>Change password</Button><Message error={change.error}/></form></details>
  {/* The cached ledger goes before the session does, so nothing stays on a shared browser. */}
  <form action="/logout" method="post" onSubmit={e => {e.preventDefault();const form=e.currentTarget;void clearCached().finally(() => form.submit())}}><input type="hidden" name="csrf" value={session.csrf}/><Button type="submit" variant="outline" size="sm">Sign out</Button></form>
 </section>
}
function SavedViews({session}:{session:Session}) {
 const client=useQueryClient(),views=useQuery(savedViewsQuery(session)),remove=useMutation({mutationFn:(id:string) => deleteView(session,id),onSuccess:() => client.invalidateQueries({queryKey:savedViewsQuery(session).queryKey})})
 if(!views.data?.length)return null
 return <section className="settings-section"><h2>Saved views</h2><p>Yours alone. Open them from search with {searchKey}.</p><ul className="tm-list static">{views.data.map(v => <li key={v.id}><div><a className="n" href={`${v.page}?${new URLSearchParams(v.state ?? {})}`}>{v.name}</a><Button size="sm" variant="outline" disabled={remove.isPending} onClick={() => remove.mutate(v.id)}>Delete</Button></div></li>)}</ul></section>
}
function Settings() {
 const {session}=Route.useRouteContext(),data=useQuery(settingsQuery(session))
 describePage({title:'Settings',lines:[]})
 return <><header className="page-header"><div><h1>Settings</h1><p className="description">Your account and the agents working for you.</p></div></header>
  <div className="settings-layout">
   <section className="settings-section"><Theme/></section>
   {data.error ? <RouteError error={data.error} reset={() => void data.refetch()} title="Settings didn’t load"/> : !data.data ? <RowsSkeleton rows={6}/> : <><Agents session={session} data={data.data}/><Feeds data={data.data}/>{session.user.is_admin && <Invite session={session}/>}</>}
   <SavedViews session={session}/>
   <Account session={session}/>
  </div>
 </>
}
