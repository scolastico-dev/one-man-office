// The question before a plugin that is not official is installed or
// updated, shown in the Plugins page's dialog scope.
import { h } from 'vue'
import { mountDialog, type DialogScope } from '../../../components/ui/mountDialog'
import type { TrustQuestion } from './pluginTrust'
import TrustDialog from './TrustDialog.vue'

/**
 * Asks before a plugin that is not official is installed or updated
 * (TrustDialog): the warning of its tag, the plugin's name, source, branch
 * and folder. Resolves true when the user agreed; false when not, or when
 * the page went away. Nothing is sent before.
 */
export function confirmInstall(q: TrustQuestion, dialogs: DialogScope | null): Promise<boolean> {
  if (dialogs === null) return Promise.resolve(false)
  return new Promise<boolean>((done) => {
    let agreed = false
    const close = mountDialog(
      (cancel) =>
        h(TrustDialog, {
          question: q,
          onCancel: cancel,
          onConfirm: () => {
            agreed = true
            close()
          },
        }),
      dialogs,
      () => done(agreed),
    )
  })
}
