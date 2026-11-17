import { mount } from '@vue/test-utils'
import { expect, test } from 'vitest'
import TicketCard from './TicketCard.vue'
import { shortenMiddle } from './model'
import { card } from './testing'

const text = (s: string) => s.replace(/\s+/g, ' ').trim()

test('TicketCard shows type, #number, title, first project and +count', () => {
  const wrapper = mount(TicketCard, {
    props: { card: card({ number: 42, title: 'Fix the login' }, { projectNames: ['ui', 'api', 'cli'] }) },
  })
  expect(wrapper.attributes('data-ticket')).toBe('42')
  expect(wrapper.find('[aria-label="Task"]').exists()).toBe(true)
  expect(wrapper.text()).toContain('#42')
  const link = wrapper.get('a')
  expect(link.attributes('href')).toBe('/ticket/42')
  expect(link.text()).toBe('Fix the login')
  expect(link.attributes('title')).toBe('Fix the login')
  const chip = wrapper.get('[data-chip="project"]')
  expect(text(chip.text())).toBe('ui +2')
  expect(chip.attributes('title')).toBe('ui, api, cli')
})

test('TicketCard for a story shows "4 of 9 tasks done" and the story edge', () => {
  const wrapper = mount(TicketCard, {
    props: { card: card({ number: 5, type: 'story', progress: { done: 4, total: 9 } }) },
  })
  expect(wrapper.classes()).toContain('card--story')
  expect(wrapper.find('[aria-label="Story"]').exists()).toBe(true)
  expect(wrapper.text()).toContain('4 of 9 tasks done')
  expect(wrapper.get('[role="progressbar"]').attributes('aria-valuenow')).toBe('4')
})

test('TicketCard agent line has a text state for assistive technology', () => {
  const wrapper = mount(TicketCard, {
    props: {
      card: card({
        column: 'in_progress',
        agents: [{ session: 's1', role: 'developer', name: 'amber-otter', state: 'running', purpose: 'work' }],
      }),
    },
  })
  const line = wrapper.get('[data-agent]')
  expect(text(line.text())).toContain('developer · amber-otter')
  expect(line.get('.sr-only').text()).toBe('running')
})

test('TicketCard story chip emits filter-story without opening the card', async () => {
  const wrapper = mount(TicketCard, {
    props: { card: card({ number: 7, story: 5 }, { storyTitle: 'Board' }) },
  })
  const chip = wrapper.get('[data-chip="story"]')
  expect(text(chip.text())).toBe('#5 Board')
  await chip.trigger('click')
  expect(wrapper.emitted('filter-story')).toEqual([[5]])
  expect(wrapper.emitted('open')).toBeUndefined()
  await wrapper.get('a').trigger('click')
  expect(wrapper.emitted('open')).toEqual([[7]])
})

test('TicketCard shows the badge in words and the caption', () => {
  const wrapper = mount(TicketCard, {
    props: {
      card: card(
        { column: 'review' },
        { badge: { tone: 'danger', text: 'needs attention', title: 'merge blocked' }, caption: 'agent review' },
      ),
    },
  })
  expect(wrapper.get('[data-badge]').text()).toContain('needs attention')
  expect(wrapper.text()).toContain('merge blocked')
  expect(wrapper.text()).toContain('agent review')
  expect(wrapper.classes()).toContain('card--danger')
})

test('shortenMiddle keeps both ends of long names', () => {
  expect(shortenMiddle('ui', 10)).toBe('ui')
  expect(shortenMiddle('github.com/acme-corp/ui', 20)).toBe('github.com/…/ui')
  expect(shortenMiddle('github.com/acme-corp/ui', 30)).toBe('github.com/acme-corp/ui')
  expect(shortenMiddle('feature/very-long-branch-name-here', 16)).toBe('feature/…me-here')
  expect(shortenMiddle('abcdefghijklmnop', 9).length).toBe(9)
})

test('TicketCard of a task without a project shows a quiet "no project" in place of the project chip', () => {
  const none = mount(TicketCard, { props: { card: card({ number: 7 }, { projectNames: [] }) } })
  expect(none.get('[data-chip="no-project"]').text()).toBe('no project')
  expect(none.find('[data-chip="project"]').exists()).toBe(false)
  // A story without projects yet shows nothing; a task with one its chip.
  expect(mount(TicketCard, { props: { card: card({ number: 8, type: 'story' }) } }).find('[data-chip="no-project"]').exists()).toBe(false)
  expect(mount(TicketCard, { props: { card: card({ number: 9 }, { projectNames: ['ui'] }) } }).find('[data-chip="no-project"]').exists()).toBe(false)
})
