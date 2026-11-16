// Dialogs are teleported to the body in the app. Unit tests look inside the
// wrapper, so dialogs render in place there; a test of the teleport itself
// provides `false` for DIALOGS_IN_PLACE.
import { config } from '@vue/test-utils'
import { afterEach } from 'vitest'
import { DIALOGS_IN_PLACE } from '../components/ui/dialogHost'
import { ticketDrafts } from '../features/ticket/drafts'

config.global.provide = { ...config.global.provide, [DIALOGS_IN_PLACE as symbol]: true }

// Unsent text is kept per ticket in module state; every test starts without any.
afterEach(() => ticketDrafts.clear())
