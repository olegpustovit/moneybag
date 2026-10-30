import { chromium } from 'playwright'
import assert from 'node:assert/strict'
const base=process.env.MONEYBAG_TEST_URL || 'http://127.0.0.1:16591'
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN,args:['--no-sandbox']})
try {
 for(const blockFont of [false,true]) {
  const context=await browser.newContext()
  if(blockFont)await context.route('**/assets/Geist-Variable-*.woff2',route=>route.abort())
  const page=await context.newPage();await page.goto(base+'/login');await page.locator('body').waitFor();await page.evaluate(()=>document.fonts.ready)
  const result=await page.evaluate(()=>{const faces=[...document.styleSheets].flatMap(sheet=>[...sheet.cssRules]).filter(rule=>rule instanceof CSSFontFaceRule),fallback=faces.find(rule=>rule.style.fontFamily.replaceAll('"','')==='Geist Fallback');return {family:getComputedStyle(document.body).fontFamily,loaded:document.fonts.check('14px Geist'),src:fallback?.style.getPropertyValue('src'),adjust:fallback?.style.getPropertyValue('size-adjust')}})
  assert(result.family.includes('Geist Fallback'));assert.equal(result.loaded,!blockFont);assert.match(result.src,/Arial/);assert(parseFloat(result.adjust)>0)
  await context.close()
 }
 console.log('authentication font and fallback passed')
} finally {await browser.close()}
