import { flushPromises } from '@vue/test-utils'
import { defineComponent, shallowRef } from 'vue'
import { useRouter, type Router } from 'vue-router'
import { afterEach, expect, test } from 'vitest'
import { harness, mountWith, ok } from '../create/dialogTesting'
import { ticket } from '../board/testing'
import { disposePluginExtensions, usePluginExtensions, usePluginTicketContext } from './usePluginExtensions'

let cleanup: (() => void) | undefined
afterEach(() => cleanup?.())
test('a ticket fetched outside the board snapshot supplies route context and leaves no stale context after navigation', async () => {
  const h = await harness()
  const base = h.session.client.get.bind(h.session.client)
  h.session.client.get = ((path: string, q?: never) => path === '/api/plugins' ? Promise.resolve(ok({ plugins: [], settings: [], errors: [] })) : base(path, q)) as typeof base
  const fetched = shallowRef(ticket({ number: 500, title: 'Archived', projects: [], workdir: '/work/500' }))
  let extensions!: ReturnType<typeof usePluginExtensions>
  let router!: Router
  const Host = defineComponent({ setup() {
    router = useRouter()
    extensions = usePluginExtensions()
    usePluginTicketContext(fetched)
    return () => null
  } })
  const w = await mountWith(h, Host, {})
  cleanup = () => { w.unmount(); disposePluginExtensions(h.session.store) }
  router.addRoute({ name: 'ticket-page', path: '/ticket/:n', component: { render: () => null } })
  await router.push({ name: 'ticket-page', params: { n: 500 } })
  await flushPromises()
  expect(h.session.store.state.tickets.has(500)).toBe(false)
  expect(extensions.context.value).toEqual({ page: 'ticket', ticket: { number: 500, title: 'Archived', projects: [], workdir: '/work/500' } })
  fetched.value = { ...fetched.value, title: 'Updated' }
  await flushPromises()
  expect(extensions.context.value.ticket?.title).toBe('Updated')
  await router.push('/')
  expect(extensions.context.value).toEqual({ page: 'board' })
})
