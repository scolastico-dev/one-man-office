// The browser tests live in web/ (see web/playwright.config.ts). This lets
// `npx --prefix web playwright test e2e/<name>.spec.ts` run from the
// repository root with the same setup and output folder (web/test-results).
export { default } from './web/playwright.config.ts'
