// `v-disabled` takes the place of `:disabled` on a button. A button that is
// disabled while it has the keyboard focus (Save while its request runs, a
// switch while its change is under way) would lose the focus to the page with
// the native attribute, so the next Tab starts again at the top. Such a button
// is marked `aria-disabled` instead and ignores activation until the focus
// leaves it; then it gets the native attribute like every other disabled
// button. `disabledGuard.test.ts` fails on a `:disabled` on a button.
import type { Directive } from 'vue'

const disabled = new WeakSet<HTMLElement>()

function block(ev: Event): void {
  const el = ev.currentTarget as HTMLElement
  if (!disabled.has(el)) return
  ev.preventDefault()
  ev.stopImmediatePropagation()
}

/** The focus left a button held as `aria-disabled`: from now on it is disabled for good. */
function release(ev: Event): void {
  const el = ev.currentTarget as HTMLButtonElement
  if (disabled.has(el) && el.getAttribute('aria-disabled') === 'true') {
    el.removeAttribute('aria-disabled')
    el.disabled = true
  }
}

function apply(el: HTMLButtonElement, on: boolean): void {
  if (!on) {
    disabled.delete(el)
    el.disabled = false
    el.removeAttribute('aria-disabled')
    return
  }
  disabled.add(el)
  if (typeof document !== 'undefined' && document.activeElement === el) {
    el.disabled = false
    el.setAttribute('aria-disabled', 'true')
  } else {
    el.removeAttribute('aria-disabled')
    el.disabled = true
  }
}

export const vDisabled: Directive<HTMLButtonElement, boolean | undefined> = {
  mounted(el, binding) {
    el.addEventListener('click', block, true)
    el.addEventListener('focusout', release)
    apply(el, binding.value === true)
  },
  updated(el, binding) {
    if ((binding.value === true) !== disabled.has(el)) apply(el, binding.value === true)
  },
  unmounted(el) {
    el.removeEventListener('click', block, true)
    el.removeEventListener('focusout', release)
    disabled.delete(el)
  },
}
