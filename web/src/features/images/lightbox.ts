import type { InlineImages } from './inlineImages'
import type { LightboxImage } from './ImageLightbox.vue'

/** The image a click in rendered Markdown opens, or null when it did not land on a shown uploaded image. */
export function lightboxFor(target: EventTarget | null, images: InlineImages): LightboxImage | null {
  const button = (target as Element | null)?.closest?.('button[data-omo-image]')
  if (!button) return null
  const id = button.getAttribute('data-omo-image') ?? ''
  const view = images.view(id)
  if (view.state !== 'shown') return null
  return { url: view.url, alt: button.querySelector('img')?.getAttribute('alt') ?? '' }
}
