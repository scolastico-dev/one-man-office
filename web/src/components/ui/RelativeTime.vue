<script setup lang="ts">
// A point in time as "3 min ago", with the full local date and time in the
// tooltip and the exact value in `datetime`. Follows the clock each minute.
import { computed } from 'vue'
import { timeKey } from '../../api/time'
import { useNow } from './useNow'

const props = defineProps<{
  /** RFC 3339, as the server writes it. */
  at: string
}>()

const MINUTE = 60_000
const HOUR = 60 * MINUTE
const DAY = 24 * HOUR
/** Older than this shows the date instead of a count of days. */
const DAYS_SHOWN = 7

const now = useNow()

/** Milliseconds since the epoch, or null for text that is no RFC 3339 time. */
const ms = computed(() => {
  const key = timeKey(props.at)
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{9}Z$/.test(key)) return null
  // Date reads milliseconds; the key's later digits of the fraction do not change the words.
  const t = Date.parse(`${key.slice(0, 23)}Z`)
  return Number.isNaN(t) ? null : t
})

const words = computed(() => {
  const t = ms.value
  if (t === null) return props.at
  const ago = now.value - t
  // A clock a little behind the server's must not read as the future.
  if (ago < MINUTE) return 'just now'
  if (ago < HOUR) return `${Math.floor(ago / MINUTE)} min ago`
  if (ago < DAY) return `${Math.floor(ago / HOUR)} h ago`
  const days = Math.floor(ago / DAY)
  if (days <= DAYS_SHOWN) return days === 1 ? '1 day ago' : `${days} days ago`
  return new Date(t).toLocaleDateString(undefined, { dateStyle: 'medium' })
})

const full = computed(() =>
  ms.value === null ? undefined : new Date(ms.value).toLocaleString(undefined, { dateStyle: 'full', timeStyle: 'medium' }),
)
</script>

<template>
  <time :datetime="at" :title="full">{{ words }}</time>
</template>
