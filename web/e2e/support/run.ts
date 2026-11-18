import { spawn, type ChildProcess } from 'node:child_process'

export interface CliResult {
  code: number
  stdout: string
  stderr: string
}

/**
 * Runs a program of the browser tests (the omo binary or the seeding tool, both
 * started directly: neither is a launcher in front of another program) to its end.
 * A program that is still running after `timeoutMs` (or when `limit` aborts) is
 * killed, and the rejection comes only once the process is closed, so a test never
 * goes on while its program still writes to the home. A kill that fails arrives as
 * the child's `error` event and rejects as well; `child.kill` answers false only
 * for a process that is already gone. `kill` is a test seam that replaces the kill
 * (it may hold it back): the run must not settle before the process is closed.
 */
export function run(
  bin: string,
  args: string[],
  env: NodeJS.ProcessEnv,
  stdin?: string,
  timeoutMs = 60_000,
  limit?: AbortSignal,
  kill: (child: ChildProcess) => void = (child) => { child.kill('SIGKILL') },
): Promise<CliResult> {
  return new Promise((res, rej) => {
    const child = spawn(bin, args, { env, stdio: ['pipe', 'pipe', 'pipe'] })
    let stdout = ''
    let stderr = ''
    let late = false
    const expire = () => {
      late = true
      kill(child)
    }
    const timer = setTimeout(expire, timeoutMs)
    limit?.addEventListener('abort', () => {
      clearTimeout(timer)
      expire()
    })
    child.stdout.on('data', (d: Buffer) => (stdout += d.toString()))
    child.stderr.on('data', (d: Buffer) => (stderr += d.toString()))
    child.once('error', (e) => {
      clearTimeout(timer)
      rej(e)
    })
    child.once('close', (code) => {
      clearTimeout(timer)
      if (late) rej(new Error(`${args.join(' ')} did not finish within ${timeoutMs} ms\n${stdout}${stderr}`))
      else res({ code: code ?? -1, stdout, stderr })
    })
    // A command that finishes without reading its input closes the pipe
    // while the write is still on its way (more often on a busy machine);
    // that is not a failure of the command.
    child.stdin.on('error', (e: NodeJS.ErrnoException) => {
      if (e.code !== 'EPIPE') {
        clearTimeout(timer)
        rej(e)
      }
    })
    child.stdin.end(stdin ?? '')
  })
}
