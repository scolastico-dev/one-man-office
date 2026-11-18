export interface FixtureRun {
  /** What the helper printed on its PROXY_BROWSER_FIXTURES line. */
  fixtures: Array<Record<string, string>>
  /** Everything the helper printed so far, for a failure message. */
  output(): string
  /** Asks the helper to finish (its own stop address), waits for the process to be closed and its build folder removed, and answers its exit code. Idempotent. */
  stop(stopUrl?: string): Promise<number | null>
}
export interface FixtureOptions {
  /** A program to run as the helper. Without one, the Go test binary of `build.pkg` is built into a folder of its own and run directly. */
  command?: string
  /** The program's arguments; for the built binary they default to the proxy browser fixture test. */
  args?: string[]
  /** The repository root: the build's working folder, and `build.pkg` is below it, where the built binary runs. */
  cwd?: string | URL
  build?: { pkg?: string }
  /** Where the build folder is made; default the system's temporary folder as configured. */
  tmp?: string
  env?: NodeJS.ProcessEnv
  /** Test seam: replaces the kill steps of `endProcess` for the helper. */
  end?: EndOptions
  /** Called with the process id as soon as the helper is started. */
  onSpawn?: (pid: number) => void
  /** Called after each piece of output with everything so far and a way to write to the helper's input. */
  onOutput?: (output: string, send: (text: string) => void) => void
}
/** The first complete line of `output` (ended by a newline) that starts with `prefix`, without its line end; an unterminated tail is never a line. */
export function completeLine(output: string, prefix: string): string | undefined
export interface EndOptions {
  tree?: boolean
  platform?: NodeJS.Platform
  killTree?: (pid: number) => void
  killGroup?: (pid: number) => void
  /** Replaces the whole platform step on every platform (a test seam that fails or holds the kill); it is called only for a process that is still running. */
  kill?: (child: { pid?: number; kill(signal?: string): boolean }, options: { tree: boolean }) => void
}
/** Ends a running process without waiting for it (the caller waits for its close); a failure to end it throws. */
export function endProcess(child: { pid?: number; exitCode: number | null; signalCode: string | null; kill(signal?: string): boolean }, options?: EndOptions): void
/** True for a program that launches others (go, npm, npx, a shell); `startFixture` refuses it as the helper. */
export function isLauncher(command: string): boolean
export function taskkillArgs(pid: number): string[]
export function fixtureEnv(base?: NodeJS.ProcessEnv, extra?: Record<string, string>, platform?: NodeJS.Platform): NodeJS.ProcessEnv
export function startFixture(options?: FixtureOptions): Promise<FixtureRun>
