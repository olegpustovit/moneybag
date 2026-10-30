import { readFileSync, readdirSync } from 'node:fs'
import { gzipSync } from 'node:zlib'

const dist = new URL('../dist/', import.meta.url)
const gzipSize = (file: string) => gzipSync(readFileSync(new URL(file, dist))).length
const html = readFileSync(new URL('index.html', dist), 'utf8')
const initial = [...html.matchAll(/(?:src|href)="\/(assets\/[^"]+\.(?:js|css))"/g)].map((match) => match[1])
if (!initial.some((name) => name.endsWith('.js'))) throw new Error('index.html does not reference a JS entry')
const size = (ext: string) => initial.filter((name) => name.endsWith(ext)).reduce((total, name) => total + gzipSize(name), 0)
const failures: string[] = []
const css = initial.filter((name) => name.endsWith('.css')).map((name) => readFileSync(new URL(name, dist), 'utf8')).join('\n')
const fallback = css.match(/@font-face\s*\{[^}]*font-family:\s*["']?Geist Fallback["']?;[^}]*}/)?.[0] ?? ''
if (!/local\(["']?Arial["']?\)/.test(fallback) || !/size-adjust:\s*[\d.]+%/.test(fallback) || !/ascent-override:\s*[\d.]+%/.test(fallback) || !/descent-override:\s*[\d.]+%/.test(fallback)) failures.push('Geist metric-adjusted Arial fallback missing from CSS')
if (!/font-family:\s*Geist,\s*["']?Geist Fallback/.test(css)) failures.push('Geist fallback missing from font stack')
if (size('.js') > 170_000) failures.push(`entry JS ${size('.js')} B > 170 KB`)
if (size('.css') > 30_000) failures.push(`entry CSS ${size('.css')} B > 30 KB`)
for (const name of readdirSync(new URL('assets/', dist))) {
  if (name.endsWith('.js') && !initial.includes(`assets/${name}`)) {
    const limit = name.startsWith('editor-') ? 160_000 : name.startsWith('chart-') ? 120_000 : 100_000
    if (gzipSize(`assets/${name}`) > limit) failures.push(`chunk ${name} exceeds ${limit / 1000} KB`)
  }
}
if (failures.length) { console.error(failures.join('\n')); process.exit(1) }
console.log(`Entry: JS ${size('.js')} B, CSS ${size('.css')} B gzip`)
