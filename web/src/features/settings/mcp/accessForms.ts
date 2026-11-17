// What the Access section offers for a new personal access token.
import type { SelectOption } from '../../../components/ui/SelectField.vue'

export const EXPIRY_OPTIONS: SelectOption[] = [
  { value: 'never', label: 'Until revoked' },
  { value: '30', label: '30 days' },
  { value: '90', label: '90 days' },
  { value: '365', label: 'One year' },
]

/** The end of a token valid for the chosen days from `now`, as the server takes it; null for "until revoked". */
export function expiresAt(choice: string, now: Date): string | null {
  const days = Number(choice)
  if (choice === 'never' || !Number.isInteger(days) || days <= 0) return null
  return new Date(now.getTime() + days * 24 * 60 * 60 * 1000).toISOString()
}
