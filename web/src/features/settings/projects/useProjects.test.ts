import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { ApiEvent, RootScan, ScanReport } from '../../../api/types'
import { harness, mountWith, ok, refuse } from '../../create/dialogTesting'
import ScanStatus from './ScanStatus.vue'
import { answerReads } from './projectsTesting'
import { useFolderPicker } from './useFolderPicker'
import { DEFAULT_IGNORE, newRoot, originText, pathBelow, rootCounts, scanSummary, sortProjects, watchesRoots } from './useProjects'
import { project } from '../../../testing/fakeOmo'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

const rootScan = (o: Partial<RootScan> = {}): RootScan => ({ path: '/w', state: 'ok', message: '', visited: 12, found: 2, added: [], skipped: [], links: 0, ...o })
const report = (o: Partial<ScanReport> = {}): ScanReport => ({
  trigger: 'button',
  started_at: '2026-11-15T10:00:00Z',
  finished_at: '2026-11-15T10:00:01Z',
  roots: [rootScan()],
  added: [],
  missing: 0,
  ...o,
})

test('a new workspace folder starts with depth 4 and ignores dependency and hidden folders', () => {
  expect(newRoot('/w')).toEqual({ path: '/w', max_depth: 4, ignore: ['node_modules', '.*'] })
  expect(newRoot('/w').ignore).not.toBe(DEFAULT_IGNORE)
})

test('the folders follow changes of settings.yaml only', () => {
  const ev = (kind: string, data: unknown) => ({ id: 1, kind, ticket: null, actor: { role: 'user' }, at: '', data }) as ApiEvent
  expect(watchesRoots(ev('settings.changed', { section: 'general', name: 'settings', source: 'disk' }))).toBe(true)
  expect(watchesRoots(ev('settings.changed', { section: 'profiles', name: 'claude', source: 'app' }))).toBe(false)
  expect(watchesRoots(ev('settings.invalid', { file: '/h/omo/settings.yaml', message: 'x' }))).toBe(true)
  expect(watchesRoots(ev('settings.invalid', { file: '/h/omo/profiles/a.yaml', message: 'x' }))).toBe(false)
  expect(watchesRoots(ev('project.changed', {}))).toBe(false)
})

test('projects sort by name; their origin is told in words', () => {
  expect(sortProjects([project('b', 'Beta'), project('a', 'alpha'), project('c', 'alpha')]).map((p) => p.id)).toEqual(['a', 'c', 'b'])
  expect(originText({ root: '' })).toBe('added by hand')
  expect(originText({ root: '~/workspace' })).toBe('found in ~/workspace')
})

test('the path of a project below its workspace folder, also for folders written with ~', () => {
  expect(pathBelow('/w/a/one', '/w', null)).toBe('a/one')
  expect(pathBelow('/w/a/one', '/w/', null)).toBe('a/one')
  expect(pathBelow('/home/you/work/x', '~/work', '/home/you')).toBe('x')
  expect(pathBelow('/home/you/work/x', '~/work', null)).toBeNull()
  expect(pathBelow('/wx/a', '/w', null)).toBeNull()
  expect(pathBelow('/w', '/w', null)).toBeNull()
})

test('scans in words', () => {
  expect(scanSummary(report({ added: ['a', 'b'] }))).toBe('2 added')
  expect(scanSummary(report({ missing: 1, roots: [rootScan(), rootScan({ state: 'cut' })] }))).toBe('nothing new, 1 missing, 1 folder with a problem')
  expect(rootCounts(rootScan({ visited: 1, found: 1, added: ['a'], links: 2 }))).toBe('1 folder looked at, 1 repository, 1 added, 2 links not followed')
})

test('the picker resolves with the chosen path or null; a second request closes the first', async () => {
  const p = useFolderPicker()
  const first = p.choose({ title: 'One', start: ' /w ' })
  expect(p.dialog.value).toEqual({ open: true, title: 'One', start: '/w' })
  const second = p.choose({ title: 'Two' })
  expect(await first).toBeNull()
  expect(p.dialog.value).toEqual({ open: true, title: 'Two' })
  p.chosen('/x')
  expect(await second).toBe('/x')
  expect(p.dialog.value.open).toBe(false)
})

test('scan status follows projects.scanned, lists skipped folders, and drops an older report', async () => {
  const h = await harness()
  answerReads(h, { scan: { running: false, last: report({ added: ['a'] }) } })
  const w = await mountWith(h, ScanStatus, {})
  mounted = w
  expect(w.get('[data-scan-line]').text()).toMatch(/^Last scan .+: 1 added$/)

  h.omo.emit(
    'projects.scanned',
    report({ finished_at: '2026-11-15T10:05:00Z', roots: [rootScan({ state: 'cut', skipped: ['/w/slow‮mount'], links: 1 })], added: [] }),
  )
  await flushPromises()
  expect(w.get('[data-scan-line]').text()).toMatch(/: nothing new, 1 folder with a problem$/)
  expect(w.findAll('[data-scan-skipped]').map((s) => s.text())).toEqual(['/w/slow\\u{202E}mount'])
  expect(w.get('[data-scan-details]').text()).toContain('stopped at its limit')
  expect(w.get('[data-scan-details]').text()).toContain('1 link not followed')

  // An older report arriving late does not replace the newer one.
  h.omo.emit('projects.scanned', report({ finished_at: '2026-11-15T10:01:00Z', added: ['x', 'y'] }))
  await flushPromises()
  expect(w.get('[data-scan-line]').text()).not.toContain('2 added')
})

test('Rescan is busy while it runs; its report replaces the status; a refusal is shown', async () => {
  const h = await harness()
  answerReads(h)
  let finish!: () => void
  h.respond.write = () => new Promise((resolve) => (finish = () => resolve(ok(report({ finished_at: '2026-11-15T11:00:00Z', added: ['n'] })))))
  const w = await mountWith(h, ScanStatus, { roots: 0 })
  mounted = w
  expect(w.get('[data-scan-line]').text()).toBe('Add a workspace folder to look for repositories.')
  await w.get('[data-rescan]').trigger('click')
  expect(w.get('[data-rescan]').attributes('disabled')).toBeDefined()
  expect(w.get('[data-scan-line]').text()).toBe('Scanning the workspace folders…')
  finish()
  await flushPromises()
  expect(h.sent).toEqual([{ method: 'POST', path: '/api/projects/rescan', body: {} }])
  expect(w.get('[data-scan-line]').text()).toMatch(/: 1 added$/)

  h.respond.write = () => refuse('unavailable', 'omo is stopping; the scan did not finish', undefined, 503)
  await w.get('[data-rescan]').trigger('click')
  await flushPromises()
  expect(w.get('[data-scan-error]').text()).toBe('omo is stopping; the scan did not finish')
  expect(w.get('[data-scan-line]').text()).toMatch(/: 1 added$/)
})
