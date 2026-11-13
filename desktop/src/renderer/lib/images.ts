// A pasted image shows in the prompt as [image N]. The hub takes a message's
// images as a list, and the text names the first one [image 1].

export const imageToken = (n: number) => `[image ${n}]`

// As the hub's model.MaxImageSize and model.MaxImages.
export const maxImageSize = Math.floor(((5 << 20) * 3) / 4)
export const maxImages = 5

const tokenRE = /\[image (\d+)\]/g

// used are the images of the map whose token is still in the text.
export function used(text: string, images: Map<number, string>): number[] {
  const out = new Set<number>()
  for (const m of text.matchAll(tokenRE)) {
    const n = Number(m[1])
    if (images.has(n)) out.add(n)
  }
  return [...out].sort((a, b) => a - b)
}

// nextImage is one past the highest number in use, so a token deleted and
// typed again never names another image.
export function nextImage(images: Map<number, string>): number {
  return Math.max(0, ...images.keys()) + 1
}

// withImages is what goes to the hub: the images whose token is in the text,
// and the text with their tokens numbered 1, 2, … in the same order.
export function withImages(text: string, images: Map<number, string>): { text: string; images: string[] } {
  const keep = used(text, images)
  const to = new Map(keep.map((n, i) => [n, i + 1]))
  return {
    text: text.replace(tokenRE, (tok, n) => (to.has(Number(n)) ? imageToken(to.get(Number(n))!) : tok)),
    images: keep.map((n) => images.get(n)!),
  }
}

// longEdge is the size the model works at; a bigger image costs more and
// shows it nothing more.
const longEdge = 1568

// fit reads a pasted image as base64. One that is too big is made smaller,
// as PNG, then as JPEG if that is still too big.
export async function fit(file: Blob): Promise<string> {
  const bmp = await createImageBitmap(file)
  const scale = Math.min(1, longEdge / Math.max(bmp.width, bmp.height))
  let blob: Blob = file
  if (scale < 1 || file.size > maxImageSize || !/^image\/(png|jpeg|gif|webp)$/.test(file.type)) {
    const c = new OffscreenCanvas(Math.round(bmp.width * scale), Math.round(bmp.height * scale))
    c.getContext('2d')!.drawImage(bmp, 0, 0, c.width, c.height)
    blob = await c.convertToBlob({ type: 'image/png' })
    if (blob.size > maxImageSize) blob = await c.convertToBlob({ type: 'image/jpeg', quality: 0.85 })
  }
  bmp.close()
  if (blob.size > maxImageSize) throw new Error('the image is too big')
  const bytes = new Uint8Array(await blob.arrayBuffer())
  let s = ''
  for (let i = 0; i < bytes.length; i += 0x8000) s += String.fromCharCode(...bytes.subarray(i, i + 0x8000))
  return btoa(s)
}
