/// <reference lib="dom" />
// The README's graphics: renders every hand-written source in
// .github/assets/src/*.svg in the dark and the light theme of the web app
// (its tokens, its font, its logo) to .github/assets/<name>-dark.svg and
// <name>-light.svg, with the lettering turned into outlines so GitHub, which
// loads no fonts for an image, shows them as drawn. Also writes previews on
// GitHub's own page colours at README width to
// web/screenshots/readme-<name>-<theme>.png and readme-all-<theme>.png.
//
// How: Chromium (Playwright's) draws a source with the app's font and prints
// it to PDF, and pdftocairo (poppler) turns the PDF into SVG paths. Needs
// Playwright's Chromium and pdftocairo. Run with `npm run readme-art`
// (or `make readme-art`); `npm run readme-art -- hero` renders only the
// sources whose name contains "hero". A new graphic is one more file in src/.
import { execFileSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { chromium, type Page } from '@playwright/test'

const web = join(dirname(fileURLToPath(import.meta.url)), '..')
const assets = join(web, '..', '.github', 'assets')
const src = join(assets, 'src')
const previews = join(web, 'screenshots')
const themes = ['dark', 'light'] as const
type Theme = (typeof themes)[number]

/** GitHub's README page colours and a common content width. */
const github: Record<Theme, string> = { dark: '#0d1117', light: '#ffffff' }
const readmeWidth = 880
/** Pixels of the logo per unit of the graphic's own size, so it stays sharp on a 2x screen at full width. */
const logoScale = 1.5

const b64 = (file: string) => readFileSync(file).toString('base64')
const fontFaces = [
  ['400', 'normal', 'JetBrainsMono-Regular'],
  ['700', 'normal', 'JetBrainsMono-Bold'],
  ['400', 'italic', 'JetBrainsMono-Italic'],
]
  .map(
    ([weight, style, file]) =>
      `@font-face{font-family:"JetBrains Mono";font-weight:${weight};font-style:${style};src:url(data:font/woff2;base64,${b64(join(web, 'src', 'assets', 'fonts', `${file}.woff2`))}) format("woff2")}`,
  )
  .join('\n')
const tokens = readFileSync(join(web, 'src', 'styles', 'tokens.css'), 'utf8')
const art = readFileSync(join(src, 'art.css'), 'utf8')
const logo = `data:image/png;base64,${b64(join(web, 'src', 'assets', 'logo.png'))}`

/** A source's place in the overview preview: its root's data-order (the order of the README). */
const order = (name: string) => Number(/<svg[^>]*\sdata-order="(\d+)"/.exec(readFileSync(join(src, `${name}.svg`), 'utf8'))?.[1] ?? 99)

const only = process.argv[2]
const sources = readdirSync(src)
  .filter((f) => f.endsWith('.svg'))
  .map((f) => f.slice(0, -4))
  .filter((name) => !only || name.includes(only))
  .sort((a, b) => order(a) - order(b) || a.localeCompare(b))
if (sources.length === 0) throw new Error(`no source in ${src}${only ? ` matches "${only}"` : ''}`)

/** The logo at `width` x `height` pixels: white line art on dark, in the text colour on light. */
async function logoFor(page: Page, width: number, height: number, theme: Theme): Promise<string> {
  return page.evaluate(
    async ({ logo, width, height, recolour }) => {
      const img = new Image()
      img.src = logo
      await img.decode()
      const canvas = document.createElement('canvas')
      canvas.width = width
      canvas.height = height
      const ctx = canvas.getContext('2d')!
      ctx.imageSmoothingQuality = 'high'
      ctx.drawImage(img, 0, 0, width, height)
      if (recolour) {
        ctx.globalCompositeOperation = 'source-in'
        ctx.fillStyle = getComputedStyle(document.documentElement).getPropertyValue('--omo-fg').trim()
        ctx.fillRect(0, 0, width, height)
      }
      return canvas.toDataURL('image/png')
    },
    { logo, width, height, recolour: theme === 'light' },
  )
}

async function render(page: Page, name: string, theme: Theme, work: string): Promise<{ width: number; height: number }> {
  let svg = readFileSync(join(src, `${name}.svg`), 'utf8')
  const size = /<svg[^>]*\swidth="(\d+)"[^>]*\sheight="(\d+)"/.exec(svg)
  if (!size) throw new Error(`${name}.svg: the root needs width and height in pixels`)
  const width = Number(size[1])
  const height = Number(size[2])

  await page.setViewportSize({ width, height })
  await page.emulateMedia({ media: 'screen', colorScheme: theme })
  await page.setContent(
    `<!doctype html><html><head><meta charset="utf-8"><style>${fontFaces}\n${tokens}\n${art}\n@page{size:${width}px ${height}px;margin:0}html,body{margin:0;padding:0;background:transparent}svg{display:block}</style></head><body></body></html>`,
  )
  // Each logo image gets a picture of its own size, recoloured for the theme.
  const images = [...svg.matchAll(/<image href="logo"[^>]*\swidth="(\d+)"[^>]*\sheight="(\d+)"/g)]
  for (const m of images) {
    const data = await logoFor(page, Math.round(Number(m[1]) * logoScale), Math.round(Number(m[2]) * logoScale), theme)
    svg = svg.replace(m[0], m[0].replace('href="logo"', `href="${data}"`))
  }
  await page.evaluate((svg) => {
    document.body.innerHTML = svg
  }, svg)
  await page.evaluate(async () => {
    for (const face of ['400 16px', '700 16px', 'italic 400 16px']) await document.fonts.load(`${face} "JetBrains Mono"`)
    await document.fonts.ready
  })

  const pdf = join(work, `${name}-${theme}.pdf`)
  await page.pdf({ path: pdf, width: `${width}px`, height: `${height}px`, printBackground: true, pageRanges: '1' })
  const out = join(assets, `${name}-${theme}.svg`)
  execFileSync('pdftocairo', ['-svg', pdf, out])
  // pdftocairo sizes the drawing in points; the README wants pixels.
  let drawn = readFileSync(out, 'utf8')
  drawn = drawn.replace(/(<svg[^>]*?)\swidth="[^"]*"\s+height="[^"]*"/, `$1 width="${width}" height="${height}"`)
  writeFileSync(out, drawn)
  return { width, height }
}

/** Screenshots the drawn graphics on GitHub's page colour at README width. */
async function preview(page: Page, names: string[], theme: Theme, file: string): Promise<void> {
  const imgs = names.map((n) => `<img src="${pathToFileURL(join(assets, `${n}-${theme}.svg`)).href}" alt="">`).join('')
  const html = join(previews, `.readme-${theme}.html`)
  writeFileSync(
    html,
    `<!doctype html><html><head><meta charset="utf-8"><style>html,body{margin:0;background:${github[theme]}}main{width:${readmeWidth}px;padding:24px 32px;display:flex;flex-direction:column;gap:24px}img{display:block;width:100%;height:auto}</style></head><body><main>${imgs}</main></body></html>`,
  )
  await page.setViewportSize({ width: readmeWidth + 64, height: 200 })
  await page.goto(pathToFileURL(html).href)
  await page.evaluate(() => Promise.all([...document.images].map((i) => i.decode())))
  await page.locator('main').screenshot({ path: join(previews, file) })
  rmSync(html)
}

mkdirSync(previews, { recursive: true })
const work = mkdtempSync(join(tmpdir(), 'omo-readme-art-'))
const browser = await chromium.launch()
try {
  const page = await (await browser.newContext({ deviceScaleFactor: 2 })).newPage()
  for (const name of sources) {
    for (const theme of themes) {
      const { width, height } = await render(page, name, theme, work)
      console.log(`wrote .github/assets/${name}-${theme}.svg (${width}x${height})`)
    }
  }
  for (const theme of themes) {
    for (const name of sources) {
      await preview(page, [name], theme, `readme-${name}-${theme}.png`)
    }
    if (!only) await preview(page, sources, theme, `readme-all-${theme}.png`)
  }
  console.log(`wrote the previews to web/screenshots/readme-*.png`)
} finally {
  await browser.close()
  rmSync(work, { recursive: true, force: true })
}
