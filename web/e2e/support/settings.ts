import { expect, type Page } from '@playwright/test'
import type { SettingsSection } from '../../src/locations'

// Each marker is rendered only after its form/list has loaded. A title, or
// the absence of one particular loading message, can precede all this content.
const CONTENT: Record<SettingsSection, readonly string[]> = {
  projects: ['[data-workspace-roots]', '[data-projects], [data-no-projects]'],
  agents: ['[data-profile-list], [data-no-profiles]'],
  roles: ['[data-role-editor="ceo"] [data-assignment]', '[data-role-editor="pm"] [data-assignment]', '[data-role-editor="developer"] [data-assignment]', '[data-role-editor="reviewer"] [data-assignment]'],
  usage: ['[data-usage-limits] input', '[data-script-list], [data-no-scripts]'],
  prompts: ['[data-prompt-row]'],
  skills: ['[data-skillset-list], [data-no-skillsets]'],
  mcp: ['[data-mcp-server-list], [data-no-mcp-servers]', '[data-mcp-enabled]', '[data-mcp-clients], [data-no-mcp-clients]', '[data-mcp-tokens], [data-no-mcp-tokens]', '[data-mcp-takeover], [data-no-takeover]'],
  environment: ['[data-env-table]'],
  limits: ['[data-field="limits.tasks"]'],
  review: ['[data-field="review_defaults.story"]'],
  plugins: ['ul[data-plugin-list], [data-no-plugins]', '[data-catalog-error], [data-catalog-empty], [data-catalog-plugin]'],
  general: ['[data-branches-section]'],
  backup: ['[data-export]'],
}

/** Waits for actual settings content before measuring or inspecting the page. */
export async function expectSettingsReady(page: Page, section: SettingsSection): Promise<void> {
  const root = page.locator(`[data-settings-section="${section}"] [data-settings-page]`)
  await expect(root).toBeVisible()
  for (const selector of CONTENT[section]) await expect(root.locator(selector).first()).toBeVisible()
}
