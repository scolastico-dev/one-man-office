// What the take-over shows and checks, as plain functions. The server's
// preview masks values ("***"); they are shown as they come.
import type { MCPServer, MCPTakeoverSource } from '../../../api/types'
import { cliLabel, commandLine, MCP_NAME_PATTERN, MCP_NAME_RULE } from './mcpForms'

/** A target id ("claude", "codex-1a2b3c4d") in words. */
export function targetIdWords(id: string): string {
  const cli = id.split('-')[0] ?? id
  return id === cli ? `${cliLabel(cli)}` : `${cliLabel(cli)} (${id})`
}

/** Where a candidate comes from: "From Claude Code: entry github; Codex (codex-1a2b): entry gh". */
export function sourceWords(sources: readonly MCPTakeoverSource[]): string {
  return `From ${sources.map((s) => `${targetIdWords(s.target)}: entry ${s.entry_name}`).join('; ')}`
}

const pairs = (m: Record<string, string> | null | undefined) =>
  Object.keys(m ?? {})
    .sort((a, b) => a.localeCompare(b))
    .map((k) => `${k}: ${(m ?? {})[k]}`)
    .join(', ')

/** The lines that say what a candidate server is, values as masked by omo. */
export function takeoverDetails(s: MCPServer): string[] {
  const out: string[] = []
  if (s.transport === 'stdio') {
    out.push(`Runs: ${commandLine([s.command, ...(s.args ?? [])])}`)
    if (s.cwd) out.push(`In: ${s.cwd}`)
    if (Object.keys(s.env ?? {}).length > 0) out.push(`Environment: ${pairs(s.env)}`)
  } else {
    out.push(`Address (${s.transport}): ${s.url}`)
    if (Object.keys(s.headers ?? {}).length > 0) out.push(`Headers: ${pairs(s.headers)}`)
    if ((s.headers_command ?? []).length > 0) out.push(`Headers command: ${commandLine(s.headers_command)}`)
    if (s.auth === 'oauth') out.push(`Signs in with OAuth${s.oauth?.client_id ? ` as client ${s.oauth.client_id}` : ''}`)
  }
  if ((s.tools?.allow ?? []).length > 0) out.push(`Only the tools: ${s.tools.allow.join(', ')}`)
  if ((s.tools?.deny ?? []).length > 0) out.push(`Never the tools: ${s.tools.deny.join(', ')}`)
  if (!s.enabled) out.push('Switched off in the program: stays off in omo.')
  return out
}

/** The refusals of the chosen names by candidate key: the name rule, a name omo has, a name chosen twice. */
export function pickErrors(picks: readonly { key: string; name: string }[], taken: readonly string[]): Record<string, string> {
  const out: Record<string, string> = {}
  const seen = new Map<string, number>()
  for (const p of picks) seen.set(p.name.trim(), (seen.get(p.name.trim()) ?? 0) + 1)
  for (const p of picks) {
    const n = p.name.trim()
    if (!MCP_NAME_PATTERN.test(n)) out[p.key] = MCP_NAME_RULE
    else if (taken.includes(n)) out[p.key] = `omo has a server named ${n} already; choose another name.`
    else if ((seen.get(n) ?? 0) > 1) out[p.key] = `Two servers would be named ${n}; choose another name for one of them.`
  }
  return out
}
