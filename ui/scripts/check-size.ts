import { readFileSync, readdirSync } from 'node:fs'
import { gzipSync } from 'node:zlib'

const dist = new URL('../dist/', import.meta.url)
const gzipSize = (file: string) => gzipSync(readFileSync(new URL(file, dist))).length
const html = readFileSync(new URL('index.html', dist), 'utf8')
const initial = [...html.matchAll(/(?:src|href)="\/(assets\/[^"]+\.(?:js|css))"/g)].map((match) => match[1])
if (!initial.some((name) => name.endsWith('.js'))) throw new Error('index.html does not reference a JS entry')
const size = (ext: string) => initial.filter((name) => name.endsWith(ext)).reduce((total, name) => total + gzipSize(name), 0)
const failures: string[] = []
if (size('.js') > 170_000) failures.push(`entry JS ${size('.js')} B > 170 KB`)
if (size('.css') > 30_000) failures.push(`entry CSS ${size('.css')} B > 30 KB`)
for (const name of readdirSync(new URL('assets/', dist))) {
  if (name.endsWith('.js') && !initial.includes(`assets/${name}`) && gzipSize(`assets/${name}`) > 100_000) failures.push(`chunk ${name} exceeds 100 KB`)
}
if (failures.length) { console.error(failures.join('\n')); process.exit(1) }
console.log(`Entry: JS ${size('.js')} B, CSS ${size('.css')} B gzip`)
