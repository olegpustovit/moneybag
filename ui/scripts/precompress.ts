import { readdirSync, readFileSync, writeFileSync } from 'node:fs'
import { brotliCompressSync, constants, gzipSync } from 'node:zlib'

const assets = new URL('../dist/assets/', import.meta.url)
for (const name of readdirSync(assets)) {
  if (!/\.(?:js|css|svg)$/.test(name)) continue
  const data = readFileSync(new URL(name, assets))
  writeFileSync(new URL(`${name}.gz`, assets), gzipSync(data, { level: 9 }))
  writeFileSync(new URL(`${name}.br`, assets), brotliCompressSync(data, { params: { [constants.BROTLI_PARAM_QUALITY]: 11 } }))
}
