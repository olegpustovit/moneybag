import { queryOptions } from '@tanstack/react-query'
import type { MoneybagTools } from './tools'

export interface Session { user: { id: string; name: string; email: string; is_admin: boolean }; csrf: string; version: string; mcp_url: string }
export class ApiError extends Error { readonly status: number; constructor(message: string, status: number) { super(message); this.status = status } }

export const sessionQuery = queryOptions({
  queryKey: ['session'],
  queryFn: async (): Promise<Session> => {
    const response = await fetch('/api/session', { credentials: 'same-origin' })
    if (!response.ok) throw new ApiError(response.status === 401 ? 'Sign in to continue.' : 'Could not load your session.', response.status)
    return response.json() as Promise<Session>
  },
  staleTime: 30_000,
})

let requestID = 0
const protocol = '2026-07-28'
type Envelope = { error?: { message: string }; result?: { isError?: boolean; structuredContent?: unknown; content?: { type: string; text?: string }[]; supportedVersions?: string[] } }
async function rpc(session: Session, method: string, params: Record<string, unknown>): Promise<NonNullable<Envelope['result']>> {
  const response = await fetch(session.mcp_url, {
    method: 'POST', credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream', 'X-CSRF-Token': session.csrf, 'MCP-Protocol-Version': protocol, 'Mcp-Method': method, ...(typeof params.name === 'string' ? { 'Mcp-Name': params.name } : {}) },
    body: JSON.stringify({ jsonrpc: '2.0', id: ++requestID, method, params: { ...params, _meta: { 'io.modelcontextprotocol/protocolVersion': protocol, 'io.modelcontextprotocol/clientInfo': { name: 'moneybag-web', version: '1' }, 'io.modelcontextprotocol/clientCapabilities': {} } } }),
  })
  if (!response.ok) throw new ApiError(response.status === 401 ? 'Your session expired. Sign in again.' : `Request failed (${response.status}).`, response.status)
  const envelope = await response.json() as Envelope
  if (envelope.error || !envelope.result) throw new Error(envelope.error?.message ?? 'The server returned an empty response.')
  return envelope.result
}
let ready: Promise<void> | undefined
export async function tool<K extends keyof MoneybagTools>(session: Session, name: K, args: MoneybagTools[K]['input']): Promise<MoneybagTools[K]['output']> {
  ready ??= rpc(session, 'server/discover', {}).then((result) => { if (!result.supportedVersions?.includes(protocol)) throw new Error('Client and server protocol versions differ.') }).catch((error: unknown) => { ready = undefined; throw error })
  await ready
  const result = await rpc(session, 'tools/call', { name, arguments: args })
  if (result.isError) throw new Error(result.content?.filter((item) => item.type === 'text').map((item) => item.text).join('\n') || 'The operation failed.')
  if (result.structuredContent !== undefined) return result.structuredContent as MoneybagTools[K]['output']
  const text = result.content?.find((item) => item.type === 'text')?.text
  if (!text) throw new Error('The server returned no result.')
  return JSON.parse(text) as MoneybagTools[K]['output']
}
