import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryHistory, createRouter, RouterProvider } from '@tanstack/react-router'
import { routeTree } from '../src/routeTree.gen'

const fixture = vi.hoisted(() => ({
 session:{user:{id:'test-user',name:'Test',email:'test@example.test',is_admin:false},csrf:'test',version:'test',mcp_url:'/api/mcp'},
 preview: [] as Array<Record<string,string>>,
}))
vi.mock('../src/lib/api', async (importOriginal) => {
 const actual=await importOriginal<typeof import('../src/lib/api')>()
 return {...actual,sessionQuery:{queryKey:['session'],queryFn:async () => fixture.session},tool:async (_session:unknown,name:string,args:Record<string,any>) => {
  if(name==='dashboard_list')return {items:[{id:'test',title:'Test',draft_version:0,published_version:1,revision:1}]}
  if(name==='db_list')return {items:[]}
  if(name==='dashboard_get')return {author:{id:'test-user',kind:'user'},status:'published',version:1,spec:{id:'test',title:'Test',version:1,params:{},data:{rows:{from:'postings',where:'row.date >= params.from && row.date <= params.to'}},pages:[{id:'main',layout:[{cols:[{component:'table',span:12}]}]}],components:[{id:'table',type:'table',title:'Transactions'}]}}
  if(name==='dashboard_preview') {fixture.preview.push(args.params);return {validation:{ok:true,errors:[],warnings:[],cost:1},values:{table:{type:'table',title:'Transactions',columns:[{key:'name',label:'Name',format:'text',align:'left'},{key:'amount',label:'Amount',format:'number',align:'right'}],rows:[{name:{v:'A',t:'A'},amount:{v:2,t:'2'}},{name:{v:'A',t:'A'},amount:{v:1,t:'1'}},{name:{v:'B',t:'B'},amount:{v:1,t:'1'}}]}}}}
  throw new Error(`Unexpected tool ${name}`)
 }}
})
HTMLElement.prototype.scrollTo = () => {}
afterEach(() => {cleanup();localStorage.clear();vi.unstubAllGlobals();fixture.preview.length=0})

it('retains multi-sort and date range in the URL and restores them on reopen', async () => {
 vi.stubGlobal('scrollTo',() => {})
 vi.stubGlobal('fetch',vi.fn(async () => new Response(JSON.stringify({count:0}),{status:200,headers:{'Content-Type':'application/json'}})))
 function open(url:string) {
  const history=createMemoryHistory({initialEntries:[url]})
  const queryClient=new QueryClient({defaultOptions:{queries:{retry:false}}})
  const router=createRouter({routeTree,history,context:{queryClient},scrollRestoration:false})
  const view=render(<QueryClientProvider client={queryClient}><RouterProvider router={router}/></QueryClientProvider>)
  return {history,queryClient,view}
 }
 const first=open('/dashboards/test?from=2024-02-01&to=2024-02-29')
 const region=await screen.findByRole('region',{name:'Transactions table'},{timeout:10000})
 fireEvent.click(within(region).getByRole('button',{name:'Name'}))
 await waitFor(() => expect(new URLSearchParams(first.history.location.search).get('sort')).toBe('name'))
 fireEvent.click(within(region).getByRole('button',{name:'Amount'}),{shiftKey:true})
 await waitFor(() => expect(new URLSearchParams(first.history.location.search).get('sort')).toBe('name,amount'))
 fireEvent.click(screen.getByRole('button',{name:'Next date range'}))
 await waitFor(() => {const p=new URLSearchParams(first.history.location.search);expect(p.get('sort')).toBe('name,amount');expect(p.get('sort_table')).toBe('table');expect(p.get('from')).toBe('2024-03-01');expect(p.get('to')).toBe('2024-03-31')})
 await waitFor(() => expect(fixture.preview).toContainEqual({from:'2024-03-01',to:'2024-03-31'}))
 const href=first.history.location.href
 first.view.unmount();first.queryClient.clear()
 const second=open(href)
 const restored=await screen.findByRole('region',{name:'Transactions table'},{timeout:10000})
 await waitFor(() => expect(within(restored).getByRole('columnheader',{name:/Amount/}).getAttribute('aria-sort')).toBe('ascending'))
 expect(within(restored).getByRole('columnheader',{name:/Name/}).getAttribute('aria-sort')).toBe('ascending')
 expect([...restored.querySelectorAll('tbody tr')].map(row => row.textContent)).toEqual(['A1','A2','B1'])
 expect(screen.getByRole('button',{name:/1 Mar 2024.*31 Mar 2024/})).toBeTruthy()
 expect(new URLSearchParams(second.history.location.search).get('sort')).toBe('name,amount')
 second.view.unmount();second.queryClient.clear()
})
