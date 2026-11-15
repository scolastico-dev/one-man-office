import { execFileSync } from 'node:child_process'
import { devNull } from 'node:os'
import { expect, test } from './support/omo'
import { card, expectCardIn, openBoard } from './support/board'
import { agentProcesses, expectNoAgentProcesses, liveAgents, script, waitForNoLiveSession, waitForSession } from './support/agents'

function git(repo: string, ...args: string[]): string {
  return execFileSync('git', ['-C', repo, ...args], {
    encoding: 'utf8',
    env: { ...process.env, GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull },
  })
}

test.describe('a developer that works and stays', () => {
  test.use({
    omoOptions: {
      agents: {
        developer: script('ready', 'write|hello.txt|hi', 'commit|feat: hello', 'print|FAKE-DEVELOPER-IS-HERE', 'hang'),
      },
    },
  })

  test('an approved standalone task gets a running fake developer that commits in its worktree', async ({ page, omo, repo }) => {
    await openBoard(page, omo)
    const n = await omo.createTicket({ title: 'Say hello' })
    await omo.moveTicket(n, 'ready')

    const session = await waitForSession(omo, n, 'developer')
    expect(session.terminal_id).toBeTruthy()
    await expectCardIn(page, n, 'In Progress')
    await expect(card(page, n).locator('[data-agent]')).toContainText('developer')

    // The commit is the script's own, made in the ticket's worktree.
    const ticket = await omo.api<{ projects: { branch: string }[] }>('GET', `/api/tickets/${n}`)
    const branch = ticket.projects[0]!.branch
    expect(branch).toMatch(new RegExp(`^omo/${n}-`))
    await expect.poll(() => git(repo, 'log', '--format=%s', branch)).toContain('feat: hello')

    // The process check sees the running agent, so an empty result later means something.
    if (process.platform === 'linux') expect(agentProcesses(omo).length).toBeGreaterThan(0)

    // Stopping the server ends the agent and leaves no process behind.
    await expectNoAgentProcesses(omo)
  })
})

test.describe('a developer that asks', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', 'ask|Which colour should it be?', 'hang') } } })

  test('a fake developer that asks its supervisor puts a question for the user on the card', async ({ page, omo }) => {
    await openBoard(page, omo)
    const n = await omo.createTicket({ title: 'Pick a colour' })
    await omo.moveTicket(n, 'ready')
    await waitForSession(omo, n, 'developer')
    await expect(card(page, n).locator('[data-badge]')).toContainText('question for you')
    const list = await omo.api<{ tickets: { number: number; questions_for_user: number }[] }>('GET', '/api/tickets?needs_me=true')
    expect(list.tickets.map((t) => t.number)).toContain(n)
    await expectNoAgentProcesses(omo)
  })
})

test.describe('without scripts', () => {
  test.use({ omoOptions: { agents: {}, paused: true } })

  test('the fake profile serves every role while paused and starts nothing', async ({ omo }) => {
    const roles = await omo.api<{ name: string; models: string[] }[]>('GET', '/api/roles')
    for (const r of ['ceo', 'pm', 'developer', 'reviewer']) {
      expect(roles.find((x) => x.name === r)?.models).toEqual(['fake/default'])
    }
    const n = await omo.createTicket()
    await omo.moveTicket(n, 'ready')
    expect(await liveAgents(omo)).toEqual([])
    await waitForNoLiveSession(omo, n)
  })
})
