// Image files dropped or pasted into a terminal or its draft box: checked,
// stored by omo, and answered as the paths the program gets.
import type { ApiClient } from '../../api/client'
import { ApiError } from '../../api/client'
import { uploadTerminalImage } from '../../api/endpoints'
import { visibleText } from '../../components/ui/visibleText'
import { MAX_IMAGE_BYTES } from '../images/imageUploads'
import { isImageFile } from './terminalImages'

export interface StoredImage {
  path: string
  name: string
}

export interface UploadResult {
  stored: StoredImage[]
  /** What was refused or failed, in words. */
  problems: string[]
}

/** Which of the files are taken at all, with a word for each kind that is not. */
export function takeableImages(list: File[]): { images: File[]; problems: string[] } {
  const problems: string[] = []
  const images = list.filter(isImageFile)
  if (images.length < list.length) {
    problems.push(list.length === 1 ? 'Only images can be dropped here.' : 'Only images can be dropped here; the other files were left out.')
  }
  const fitting = images.filter((f) => {
    if (f.size <= MAX_IMAGE_BYTES) return true
    problems.push(`${visibleText(f.name)}: an image has at most 10 MiB.`)
    return false
  })
  return { images: fitting, problems }
}

/** Stores the images one after the other; `onStart` hears how many are going up. */
export async function uploadImages(client: ApiClient, list: File[], onStart?: (count: number) => void): Promise<UploadResult> {
  const { images, problems } = takeableImages(list)
  const stored: StoredImage[] = []
  if (images.length === 0) return { stored, problems }
  onStart?.(images.length)
  for (const file of images) {
    const name = file.name || 'pasted image.png'
    try {
      stored.push({ path: (await uploadTerminalImage(client, file, name)).data.path, name })
    } catch (e) {
      problems.push(`${visibleText(name)}: ${e instanceof ApiError ? e.message : 'omo did not take it.'}`)
    }
  }
  return { stored, problems }
}
