export interface FixtureRun {
  /** What the helper printed on its PROXY_BROWSER_FIXTURES line. */
  fixtures: Array<Record<string, string>>
  /** Everything the helper printed so far, for a failure message. */
  output(): string
  /** Asks the helper to finish (its own stop address), waits for the process to end, and answers its exit code. Idempotent. */
  stop(stopUrl?: string): Promise<number | null>
}
export interface FixtureOptions {
  command?: string
  args?: string[]
  cwd?: string | URL
  env?: NodeJS.ProcessEnv
  /** Called with the process id as soon as the helper is started. */
  onSpawn?: (pid: number) => void
}
export function fixtureEnv(base?: NodeJS.ProcessEnv, extra?: Record<string, string>, platform?: NodeJS.Platform): NodeJS.ProcessEnv
export function startFixture(options?: FixtureOptions): Promise<FixtureRun>
