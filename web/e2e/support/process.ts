import { execFileSync } from 'node:child_process'
import { readdirSync, readFileSync, statSync } from 'node:fs'

/**
 * Whether the operating system still has the process: its own table, never a task status. A process that ended but is
 * not yet collected does not count (Linux reads its state; macOS and the BSDs ask `ps`; Windows has no such state).
 */
export function processAlive(pid: number): boolean {
  try {
    if (process.platform === 'linux') {
      const stat = readFileSync(`/proc/${pid}/stat`, 'utf8')
      if (stat.slice(stat.lastIndexOf(')') + 2).startsWith('Z')) return false
    } else if (process.platform !== 'win32') {
      // A zombie still answers signal 0 on macOS and the BSDs; `ps` reports its state.
      const state = execFileSync('ps', ['-o', 'stat=', '-p', String(pid)], { stdio: ['ignore', 'pipe', 'ignore'], encoding: 'utf8' }).trim()
      if (state === '' || state.startsWith('Z')) return false
    }
    process.kill(pid, 0)
    return true
  } catch { return false }
}

/**
 * The processes of a test that still exist, from what the programs recorded themselves: ids as file names in `dir`
 * (the fakes) or as lines in a file. Asking the operating system about each id works on every platform; a missing
 * folder or file is an error, never an empty answer, so a recorder that did not run cannot read as "all ended".
 */
export function recordedProcesses(path: string): number[] {
  const stat = statSync(path)
  const ids = stat.isDirectory() ? readdirSync(path) : readFileSync(path, 'utf8').split(/\r?\n/)
  return ids.filter((id) => /^\d+$/.test(id)).map(Number).filter(processAlive)
}
