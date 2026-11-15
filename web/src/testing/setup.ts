// Dialogs are teleported to the body in the app. Unit tests look inside the
// wrapper, so dialogs render in place there; a test of the teleport itself
// provides `false` for DIALOGS_IN_PLACE.
import { config } from '@vue/test-utils'
import { DIALOGS_IN_PLACE } from '../components/ui/dialogHost'

config.global.provide = { ...config.global.provide, [DIALOGS_IN_PLACE as symbol]: true }
