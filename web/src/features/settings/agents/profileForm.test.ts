import { expect, test } from 'vitest'
import type { Profile } from '../../../api/types'
import {
  accountVariable,
  blankForm,
  duplicateProfile,
  errorsOfProfile,
  errorsUnder,
  newModel,
  rowErrors,
  toForm,
  toProfile,
  unplacedErrors,
} from './profileForm'

const stored = (): Profile =>
  ({
    name: 'claude',
    cli: 'claude',
    command: 'claude',
    args: ['--verbose'],
    env: { ANTHROPIC_API_KEY: 'sk-ant-1', HTTPS_PROXY: '$OMO_PROXY' },
    account_dir: '~/.claude-work',
    usage_script: 'claude-usage',
    prompt_delivery: { inject: false, delay: '5s' },
    models: {
      opus: { args: ['--model', 'opus'], env: { TOKEN_X: 'tok-1' }, usage_windows: ['5h', '7d'], description: 'Strongest.' },
      sonnet: { args: [], env: null, usage_windows: null, selectable: false, description: '' },
    },
    revision: 'abcdef0123456789',
    // A field this page does not know (a newer server).
    future_field: { keep: true },
  }) as unknown as Profile

test('profileForm: a save keeps usage_script, usage_windows and environments exactly as loaded', () => {
  const f = toForm(stored())
  expect(f.models.map((m) => m.name)).toEqual(['opus', 'sonnet'])
  expect(f.inject).toBe('off')
  expect(f.models[1]!.selectable).toBe(false)

  f.command = '/usr/local/bin/claude'
  f.models[0]!.description = 'Plans.'
  const body = toProfile(f) as Profile & { future_field?: unknown }

  expect(body.usage_script).toBe('claude-usage')
  expect(body.models!.opus!.usage_windows).toEqual(['5h', '7d'])
  expect(body.env).toEqual({ ANTHROPIC_API_KEY: 'sk-ant-1', HTTPS_PROXY: '$OMO_PROXY' })
  expect(body.models!.opus!.env).toEqual({ TOKEN_X: 'tok-1' })
  expect(body.future_field).toEqual({ keep: true })
  expect(body.revision).toBeUndefined()
  expect(body.command).toBe('/usr/local/bin/claude')
  expect(body.models!.opus!.description).toBe('Plans.')
  // A selectable model without a value stays without one; an explicit false stays false.
  expect('selectable' in body.models!.opus!).toBe(false)
  expect(body.models!.sonnet!.selectable).toBe(false)
  expect(body.prompt_delivery).toEqual({ inject: false, delay: '5s' })
})

test('profileForm: the round trip of an unchanged profile changes nothing but the revision', () => {
  const p = stored()
  const body = toProfile(toForm(p))
  const { revision: _r, ...rest } = p
  expect(body).toEqual({ ...rest, models: { ...rest.models, sonnet: { ...rest.models!.sonnet!, env: {} } } })
})

test('profileForm: empty delivery fields mean the program default and are left out', () => {
  const f = toForm(stored())
  f.inject = 'default'
  f.delay = ' '
  f.retries = 2
  f.retry_wait = '10s'
  expect(toProfile(f).prompt_delivery).toEqual({ retries: 2, retry_wait: '10s' })
})

test('profileForm: a new profile has one model and the command of its program', () => {
  const f = blankForm('work', 'codex')
  f.models.push(newModel('fast'))
  const body = toProfile(f)
  expect(body.name).toBe('work')
  expect(body.command).toBe('codex')
  expect(Object.keys(body.models!)).toEqual(['default', 'fast'])
  expect(body.usage_script).toBe('')
  expect(blankForm('x', 'custom').command).toBe('')
  expect(accountVariable('claude')).toBe('CLAUDE_CONFIG_DIR')
  expect(accountVariable('custom')).toBe('')
})

test('profileForm: a duplicate copies every variable under the new name', () => {
  const profile = duplicateProfile(stored(), 'claude-2')
  expect(profile.name).toBe('claude-2')
  expect(profile.revision).toBeUndefined()
  expect(profile.env).toEqual({ ANTHROPIC_API_KEY: 'sk-ant-1', HTTPS_PROXY: '$OMO_PROXY' })
  expect(profile.models!.opus!.env).toEqual({ TOKEN_X: 'tok-1' })
})

test('profileForm: field errors are placed by key, the rest goes to the top', () => {
  const fe = {
    command: 'command must not be empty',
    'env.NEW_KEY': 'Enter the value of NEW_KEY again.',
    'args.1': 'bad',
    'models.v1.5.env.FOO': 'Enter the value of FOO again.',
    'models.gone.description': 'stale',
    usage_script: 'usage_script must be the name of a script',
  }
  expect(errorsUnder(fe, 'env')).toEqual({ NEW_KEY: 'Enter the value of NEW_KEY again.' })
  expect(errorsUnder(fe, 'models.v1.5.env')).toEqual({ FOO: 'Enter the value of FOO again.' })
  expect(rowErrors(fe, 'args')).toEqual({ 1: 'bad' })
  expect(unplacedErrors(fe, ['v1.5'])).toEqual(['stale', 'usage_script must be the name of a script'])
})

test('profileForm: the broken files of one profile', () => {
  const errs = [
    { file: '/h/omo/profiles/claude.yaml', message: 'line 2: x' },
    { file: '/h/omo/profiles/codex.yaml', message: 'line 3: y' },
    { file: '/h/omo/roles/claude.yaml', message: 'z' },
  ]
  expect(errorsOfProfile(errs, 'claude')).toEqual([errs[0]])
})
