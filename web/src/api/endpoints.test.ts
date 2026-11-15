import { describe, expect, it } from 'vitest'
import type { ApiClient } from './client'
import * as e from './endpoints'

interface Seen {
  method: string
  path: string
  query?: unknown
  hasBody: boolean
  body?: unknown
}

function spy() {
  const seen: Seen[] = []
  const result = { data: undefined, eventId: null }
  const client = {
    async get(path: string, query?: unknown) {
      seen.push({ method: 'GET', path, query, hasBody: false })
      return result
    },
    async send(method: string, path: string, ...rest: unknown[]) {
      seen.push({ method, path, hasBody: rest.length > 0 && rest[0] !== undefined, body: rest[0] })
      return result
    },
    async openStream() {
      throw new Error('not used')
    },
  } as unknown as ApiClient
  return { client, seen }
}

describe('endpoints', () => {
  it('approve and cancel send {}; markMerged sends no body', async () => {
    const { client, seen } = spy()
    await e.approveTicket(client, 4)
    await e.retryTicket(client, 4)
    await e.markMerged(client, 4)
    await e.cancelTicket(client, 4, false)
    expect(seen[0]).toMatchObject({ method: 'POST', path: '/api/tickets/4/approve', body: {} })
    expect(seen[1]).toMatchObject({ method: 'POST', path: '/api/tickets/4/retry', body: {} })
    expect(seen[2]).toMatchObject({ method: 'POST', path: '/api/tickets/4/mark-merged', hasBody: false })
    expect(seen[3]).toMatchObject({ method: 'POST', path: '/api/tickets/4/cancel', body: { delete_branch: false } })
  })

  it('cancelTicket never sends discard_uncommitted', async () => {
    const { client, seen } = spy()
    await e.cancelTicket(client, 4, true)
    expect(seen[0]?.body).toEqual({ delete_branch: true })
    expect(Object.keys(seen[0]?.body as object)).toEqual(['delete_branch'])
  })

  it('updateTicket omits absent keys and sends null to clear', async () => {
    const { client, seen } = spy()
    await e.updateTicket(client, 7, { title: 'New', story: null, model: undefined })
    expect(seen[0]).toMatchObject({ method: 'PATCH', path: '/api/tickets/7' })
    expect(JSON.stringify(seen[0]?.body)).toBe('{"title":"New","story":null}')
  })

  it('moveTicket sends the column and a null before at the end', async () => {
    const { client, seen } = spy()
    await e.moveTicket(client, 2, 'review', null)
    await e.moveTicket(client, 2, 'ready', 9)
    expect(seen[0]?.body).toEqual({ column: 'review', before: null })
    expect(seen[1]?.body).toEqual({ column: 'ready', before: 9 })
    expect(seen[0]?.path).toBe('/api/tickets/2/move')
  })

  it('listTickets passes the filter as query and setDescription the base version', async () => {
    const { client, seen } = spy()
    await e.listTickets(client, { done_limit: 50, needs_me: true })
    await e.listTickets(client)
    await e.setDescription(client, 3, 'text', 5)
    expect(seen[0]?.query).toEqual({ done_limit: 50, needs_me: true })
    expect(seen[1]?.query).toEqual({})
    expect(seen[2]).toMatchObject({ method: 'PUT', path: '/api/tickets/3/description', body: { text: 'text', base_version: 5 } })
  })

  it('maps the remaining routes', async () => {
    const { client, seen } = spy()
    await e.getTicket(client, 1)
    await e.createTicket(client, { type: 'task', title: 't', projects: [] })
    await e.rejectTicket(client, 1, 'no')
    await e.addDependency(client, 1, 2)
    await e.removeDependency(client, 1, 2)
    await e.listProjects(client)
    await e.listProjects(client, true)
    await e.createProject(client, '/p')
    await e.createProject(client, '/p', 'n')
    await e.getServer(client)
    await e.setPaused(client, true)
    await e.stopAllAgents(client)
    await e.listModels(client)
    await e.getProfiles(client)
    await e.getRoles(client)
    await e.getSettings(client)
    expect(seen.map((s) => `${s.method} ${s.path}`)).toEqual([
      'GET /api/tickets/1',
      'POST /api/tickets',
      'POST /api/tickets/1/reject',
      'POST /api/tickets/1/dependencies',
      'DELETE /api/tickets/1/dependencies/2',
      'GET /api/projects',
      'GET /api/projects',
      'POST /api/projects',
      'POST /api/projects',
      'GET /api/server',
      'PUT /api/server/paused',
      'POST /api/agents/stop-all',
      'GET /api/models',
      'GET /api/profiles',
      'GET /api/roles',
      'GET /api/settings',
    ])
    expect(seen[2]?.body).toEqual({ comment: 'no' })
    expect(seen[3]?.body).toEqual({ depends_on: 2 })
    expect(seen[4]?.hasBody).toBe(false)
    expect(seen[6]?.query).toEqual({ all: true })
    expect(seen[7]?.body).toEqual({ path: '/p' })
    expect(seen[8]?.body).toEqual({ path: '/p', name: 'n' })
    expect(seen[10]?.body).toEqual({ paused: true })
    expect(seen[11]?.body).toEqual({})
  })
})
