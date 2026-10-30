// Drives the telemetry kit demo like a person would. Needs `vite --port 5199` running in ui/.
import { chromium } from 'playwright'

const base=process.env.DEMO_URL ?? 'http://127.0.0.1:5199/telemetry-demo.html'
const browser=await chromium.launch({executablePath:process.env.CHROMIUM_BIN || `${process.env.HOME}/.nix-profile/bin/chromium`,args:['--no-sandbox']})
let failed=0,passed=0
const check=(name,ok,detail='') => {if(ok)passed++;else failed++;console.log(`${ok ? 'ok  ' : 'FAIL'} ${name}${detail ? ` (${detail})` : ''}`)}
const context=await browser.newContext({viewport:{width:1440,height:900},colorScheme:'light'})
const page=await context.newPage(),errors=[]
page.on('pageerror',e => errors.push(e.message))
const open=async (query='') => {await page.goto(`${base}?theme=light${query}`);await page.locator('.u-over').first().waitFor();await page.waitForTimeout(250)}
const status=() => page.locator('.tm-time-status').innerText()
const days=async () => Number((await status()).match(/([\d,]+) days?/)[1].replace(',',''))
const param=(name) => new URL(page.url()).searchParams.get(name)
const over=(i=0) => page.locator('.u-over').nth(i)
const settle=() => page.waitForTimeout(120)

await open()
check('default range is 12mo',await days()===365)

// Drag zoom.
let box=await over().boundingBox()
await page.mouse.move(box.x+box.width*.3,box.y+60);await page.mouse.down();await page.mouse.move(box.x+box.width*.6,box.y+60,{steps:6});await page.mouse.up();await settle()
const dragged=await days();check('drag across a chart zooms the page',dragged>90 && dragged<135,`${dragged} days`)
check('zoom is saved in the URL',/^\d{4}-\d{2}-\d{2} to /.test(param('t') ?? ''),param('t'))
check('selection box is cleared after a drag',await page.locator('.u-select').first().evaluate(e => e.getBoundingClientRect().width)===0)
await page.getByRole('button',{name:'Undo zoom'}).click();await settle()
check('undo zoom restores the range',await days()===365)

// Wheel.
await over().scrollIntoViewIfNeeded();box=await over().boundingBox();await page.mouse.move(box.x+box.width/2,box.y+60)
let y0=await page.evaluate(() => scrollY)
await page.mouse.wheel(0,200);await settle()
check('plain scroll leaves the range alone',await days()===365)
check('plain scroll still scrolls the page',await page.evaluate(() => scrollY)>y0)
await page.evaluate(() => scrollTo(0,0));await settle();box=await over().boundingBox();await page.mouse.move(box.x+box.width/2,box.y+60);y0=await page.evaluate(() => scrollY)
await page.keyboard.down('Control');await page.mouse.wheel(0,-200);await page.waitForTimeout(80);await page.mouse.wheel(0,-200);await page.keyboard.up('Control');await page.waitForTimeout(200)
const wheel=await days();check('Ctrl+scroll zooms in',wheel<365,`${wheel} days`)
check('Ctrl+scroll does not scroll the page',await page.evaluate(() => scrollY)===y0)
await over().dblclick({position:{x:200,y:60}});await settle()
check('double-click steps out',await days()>wheel,`${await days()} days`)

// Crosshair and tooltip.
await open();box=await over().boundingBox();await page.mouse.move(box.x+box.width*.5,box.y+80);await settle()
const tips=await page.locator('.tm-tip').evaluateAll(all => all.filter(t => t.style.display==='block').map(t => t.innerText))
check('one tooltip, on the hovered chart only',tips.length===1,`${tips.length}`)
check('tooltip lists the date and value',/\d{4}/.test(tips[0] ?? '') && /€/.test(tips[0] ?? ''),(tips[0] ?? '').replace(/\n/g,' / '))
const cursors=await page.locator('.u-over').evaluateAll(all => all.slice(0,3).map(o => {const c=o.querySelector('.u-cursor-x');return Number(c.style.transform.match(/translate\(([-\d.]+)px/)?.[1] ?? -1)/o.clientWidth}))
check('crosshair is synced across charts',cursors.every(x => Math.abs(x-.5)<.02),cursors.map(x => x.toFixed(3)).join(' , '))

// Legend.
const legend=page.getByRole('group',{name:'Spending by category series'})
const rent=legend.getByRole('button',{name:'Rent'}),other=legend.getByRole('button',{name:'Groceries'})
await rent.click();await settle()
check('legend click isolates',await rent.getAttribute('aria-pressed')==='true' && await other.getAttribute('aria-pressed')==='false')
await rent.click();await settle();check('second click shows all again',await other.getAttribute('aria-pressed')==='true')
await rent.click({modifiers:['Shift']});await settle();check('shift-click hides one series',await rent.getAttribute('aria-pressed')==='false' && await other.getAttribute('aria-pressed')==='true')
await rent.click({modifiers:['Shift']})

// Table toggle.
const spending=page.locator('figure',{has:page.getByRole('heading',{name:'Spending by category'})})
await spending.getByRole('button',{name:'Table'}).click();await settle()
check('table toggle shows a real table',await spending.locator('table tbody tr').count()>=12)
check('table dates are human',/^[A-Z][a-z]{2} \d{4}$/.test((await spending.locator('tbody td').first().innerText()).trim()),await spending.locator('tbody td').first().innerText())
await spending.getByRole('button',{name:'Chart'}).click();await settle()
check('chart comes back from the table',await spending.locator('canvas').count()===1)

// Keyboard on the plot.
const plot=page.locator('.tm-plot').first();await plot.focus();await page.keyboard.press('ArrowRight');await page.keyboard.press('ArrowRight');await settle()
check('arrow keys read points',await page.locator('.tm-tip').first().evaluate(t => t.style.display==='block'))
check('keyboard reading reaches a live region',(await page.locator('figure').first().locator('[aria-live]').innerText()).includes('€'))
await page.keyboard.press('Escape');check('Esc clears the reading',await page.locator('.tm-tip').first().evaluate(t => t.style.display!=='block'))
await page.keyboard.press('Tab')
await page.locator('body').click({position:{x:2,y:2}})

// Time keys.
await open();await page.keyboard.press('[');await settle();const previous=param('t');check('[ steps back',!!previous && previous.startsWith('2024-'),previous)
await page.keyboard.press(']');await settle();check('] steps forward',(param('t') ?? '').endsWith('2026-10-30'),param('t'))
await page.keyboard.press('z');await settle();check('z zooms out',await days()>600,`${await days()} days`)
await page.keyboard.press('t');await settle();check('t focuses the date field',await page.evaluate(() => document.activeElement?.id)===await page.getByLabel('Time range. Type anything').getAttribute('id'))
const field=page.getByLabel('Time range. Type anything')
await field.fill('q2 2026');check('typing shows the live parse',(await status()).includes('1 Apr 2026 to 30 Jun 2026') && (await status()).includes('press Enter'),await status())
await field.fill('banana');check('bad input shows an error',await field.getAttribute('aria-invalid')==='true' && (await status()).includes('Can’t read'),await status())
await page.keyboard.press('z');check('typing z in the field is not a shortcut',(await field.inputValue()).endsWith('z'))
await field.fill('q2 2026');await page.keyboard.press('Enter');await settle();check('Enter applies the range',await days()===91 && param('t')==='q2 2026')
await page.goBack();await settle();check('browser back returns to the earlier range',await days()!==91,`${await days()} days`)
await page.getByRole('button',{name:'YTD'}).click();await settle();check('preset applies and shows pressed',await page.getByRole('button',{name:'YTD'}).getAttribute('aria-pressed')==='true' && param('t')==='ytd')
await page.goto(`${base}?theme=light&t=garbage&cmp=nope&f=nope`);await page.locator('.u-over').first().waitFor();check('malformed URL degrades to defaults',await days()===365)

// Compare.
await page.getByLabel('Compare to').selectOption('none');await settle()
check('compare off hides the overlay and says so',await page.locator('.tm-stat').filter({hasText:'Money out'}).first().innerText().then(t => t.includes('No comparison')))
await page.getByLabel('Compare to').selectOption('previous_year');await settle();check('compare mode is in the URL',param('cmp')==='previous_year')
check('sentence names the comparison',(await page.locator('.tm-answer-lead').innerText()).includes('same period last year'),await page.locator('.tm-answer-lead').innerText())

// Navigator.
await open();const win=page.locator('.tm-nav-win'),nav=page.locator('.tm-nav');let nb=await nav.boundingBox(),wb=await win.boundingBox()
await page.mouse.move(wb.x+wb.width/2,nb.y+30);await page.mouse.down();await page.mouse.move(wb.x+wb.width/2-200,nb.y+30,{steps:8});await page.waitForTimeout(60);await page.mouse.up();await settle()
check('brush moves the window and keeps its length',await days()===365 && !(param('t') ?? '').endsWith('2026-10-30'),param('t'))
const grip=page.locator('[data-grip="r"]');await grip.focus();const before=await days();await page.keyboard.press('ArrowLeft');await settle();check('grip moves by keyboard',await days()===before-7);check('grip has a readable value',/\d+ [A-Z][a-z]{2} \d{4}/.test(await grip.getAttribute('aria-valuetext')))
const gb=await grip.boundingBox();await page.mouse.move(gb.x+gb.width/2,nb.y+30);await page.mouse.down();await page.mouse.move(gb.x+gb.width/2-80,nb.y+30,{steps:5});await page.mouse.up();await settle();check('grip drag narrows the window',await days()<before-7,`${await days()} days`)
const hit=nb.x+20;await page.mouse.click(hit,nb.y+40);await settle();check('click on the track moves the window there',(param('t') ?? '').startsWith('2023-'),param('t'))
await page.getByRole('button',{name:'Undo zoom'}).click();await settle();check('undo works after brushing',!(param('t') ?? '').startsWith('2023-'),param('t'))

// Filter.
await open();await page.keyboard.press('/');const combo=page.getByRole('combobox',{name:'Add a filter'});check('/ focuses the filter',await combo.evaluate(e => e===document.activeElement))
check('combobox opens on focus',await combo.getAttribute('aria-expanded')==='true')
await page.keyboard.type('gro');await settle();const active=await combo.getAttribute('aria-activedescendant');check('active option is announced',!!active && await page.locator(`[id="${active}"]`).getAttribute('aria-selected')==='true')
const opt=await page.locator('.tm-suggest [role=option]').first().innerText();check('suggestions carry counts and totals',/entries/.test(opt) && /€/.test(opt),opt.replace(/\n/g,' '))
const stat0=await page.locator('.tm-stat-value').nth(2).innerText();await page.keyboard.press('Enter');await settle()
check('Enter commits a pill',await page.locator('.tm-pill').count()===1 && (await page.locator('.tm-pill').innerText()).includes('Groceries'))
check('filter recalculates the page',await page.locator('.tm-stat-value').nth(2).innerText()!==stat0)
check('filter is in the URL',(param('f') ?? '').startsWith('category:is:'),param('f'))
await page.locator('.tm-pill .o').click();await settle();check('click changes the operator',(await page.locator('.tm-pill .o').innerText())==='is not')
await combo.focus();await page.keyboard.type('Pay');await page.keyboard.press('ArrowDown');await settle();await page.keyboard.press('ArrowUp');await page.keyboard.press('Enter');await settle();check('choosing a field starts a draft',(await page.locator('.tm-draft').innerText())==='Payee')
await page.keyboard.press('Enter');await settle();check('choosing an operator continues',(await page.locator('.tm-draft').innerText())==='Payee contains')
await page.keyboard.type('lidl');await settle();await page.keyboard.press('Enter');await settle();check('free text becomes a pill',(await page.locator('.tm-pill').nth(1).innerText()).includes('lidl'))
await page.keyboard.press('Backspace');await settle();check('Backspace on empty removes the last pill',await page.locator('.tm-pill').count()===1)
await page.keyboard.type('zzzzz');await settle();check('no matches is stated',(await page.locator('.tm-suggest').innerText()).includes('No matches'));await page.keyboard.press('Escape');check('Esc closes the list',await combo.getAttribute('aria-expanded')==='false');await page.keyboard.press('Escape');check('second Esc clears the text',await combo.inputValue()==='')
await page.locator('.tm-pill .x').click();await settle();check('pill can be removed',await page.locator('.tm-pill').count()===0)
await page.getByRole('button',{name:'Everything except rent, no estimates'}).click();await settle();check('ask chip makes editable pills',await page.locator('.tm-pill').count()===2,(await page.locator('.tm-filter-box').innerText()).replace(/\n/g,' '))
await page.getByRole('button',{name:'Groceries over €50 last 3 months'}).click();await settle();check('ask chip also sets the time',await days()===92 && await page.locator('.tm-pill').count()===2,`${await days()} days`)
await page.getByRole('button',{name:'Advanced'}).click();const cel=page.getByLabel('Same filter as CEL',{exact:false});check('Advanced shows the same filter as CEL',(await cel.inputValue()).includes('row.category == "Groceries"') && (await cel.inputValue()).includes('row.amount > 50'),await cel.inputValue())
await cel.fill('row.category == "Rent"');await page.getByRole('button',{name:'Apply'}).click();await settle();check('editing CEL updates the pills',(await page.locator('.tm-pill').innerText()).includes('Rent'))
await cel.fill('size(row.tags) > 1');await page.getByRole('button',{name:'Apply'}).click();check('CEL pills cannot show is refused',(await page.getByRole('alert').first().innerText()).includes('needs an agent'))
await page.getByRole('button',{name:'Save view'}).click();check('save view calls back',await page.evaluate(() => window.__demo.saved)===1)
await open();const moved=page.locator('.tm-moved button').first(),movedName=await moved.locator('.n').innerText();await moved.click();await settle();check('What moved row filters',(await page.locator('.tm-pill').innerText()).includes(movedName),movedName)

// Details drawer.
await open();const opener=page.getByRole('button',{name:'How was Money out calculated?'});await opener.click();const drawer=page.getByRole('dialog');await drawer.waitFor();await page.waitForTimeout(250);const db=await drawer.boundingBox();check('drawer sits on screen',db.x>=0 && db.x+db.width<=1441,JSON.stringify(db))
check('drawer holds the formula and machine fields',(await drawer.innerText()).includes('row.class') && (await drawer.innerText()).includes('metric:spending'))
check('focus moved into the drawer',await drawer.evaluate(d => d.contains(document.activeElement)))
const trail=[];let trapped=true;for(let i=0;i<14;i++){await page.keyboard.press('Tab');await page.waitForTimeout(30);const a=await drawer.evaluate(d => [d.contains(document.activeElement),document.activeElement?.tagName+'.'+(document.activeElement?.getAttribute('data-base-ui-focus-guard')!==null ? 'guard' : document.activeElement?.className?.toString().slice(0,20))]);trail.push(a[1]);if(!a[0] && !a[1].endsWith('guard'))trapped=false}check('focus stays trapped in the drawer',trapped,trail.join(' '))
await drawer.getByRole('button',{name:'Flag this'}).click();await drawer.getByLabel('What looks wrong?',{exact:false}).fill('Too high');await drawer.getByRole('button',{name:'Send to an agent'}).click();await page.waitForTimeout(100)
check('flag sends the number and note',await page.evaluate(() => window.__demo.flags[0]?.note==='Too high' && window.__demo.flags[0]?.target?.metric==='Money out'))
await page.keyboard.press('Escape');await page.waitForTimeout(300);check('Esc closes the drawer',await drawer.count()===0);check('focus returns to the opener',await opener.evaluate(e => e===document.activeElement))

// Command bar.
await page.keyboard.press('Control+k');const cmd=page.getByRole('dialog');await cmd.waitFor();await page.waitForTimeout(250);const cb=await cmd.boundingBox();check('command bar sits on screen and centred',cb.x>=0 && Math.abs(cb.x+cb.width/2-720)<2,JSON.stringify(cb));await page.keyboard.type('lidl');await settle();await page.keyboard.press('Enter');await page.waitForTimeout(200);check('Ctrl+K jumps to a payee',(await page.locator('.demo-toast').innerText()).includes('Lidl'))
await page.keyboard.press('Control+k');await cmd.waitFor();await page.keyboard.type('why is rent up');await settle();await page.keyboard.press('Enter');await page.waitForTimeout(200);check('a question falls through to the agent',await page.evaluate(() => window.__demo.ask[0])==='why is rent up',await page.evaluate(() => JSON.stringify(window.__demo.ask)))
await page.keyboard.press('Control+k');await cmd.waitFor();await page.keyboard.type('Now');await settle();check('pages are listed',await page.getByRole('option',{name:'Now'}).count()>=1);await page.keyboard.press('Escape');await page.waitForTimeout(500);check('Esc closes the command bar',await cmd.count()===0)
await page.getByLabel('Time range. Type anything').focus();const t9=param('t');await page.keyboard.press('Control+k');await page.waitForTimeout(150);check('Ctrl+K works from a text field',await page.getByRole('dialog').count()===1);await page.keyboard.press('[');await settle();check('shortcuts sleep behind a dialog',param('t')===t9);await page.keyboard.press('Escape');await page.waitForTimeout(500)

// Grid.
await open('&t=all');const grid=page.getByRole('table',{name:'Entries'});const rowCount=Number(await grid.getAttribute('aria-rowcount'))
check('the whole ledger is in the grid',rowCount>1000,`${rowCount} rows`)
await grid.scrollIntoViewIfNeeded();check('only a window of rows is in the DOM',await grid.locator('.tm-grid-body [role=row]').count()<80,`${await grid.locator('.tm-grid-body [role=row]').count()} rows`)
const noInner=await grid.evaluate(g => {for(let e=g;e;e=e.parentElement){const o=getComputedStyle(e).overflowY;if((o==='auto'||o==='scroll') && e!==document.documentElement && e!==document.body && e.clientHeight<e.scrollHeight)return false}return true});check('the page is the scroller, no fixed viewport',noInner)
await page.evaluate(() => {const g=document.querySelector('[aria-label="Entries"]');scrollTo(0,g.getBoundingClientRect().top+scrollY+3000)});await settle();await page.waitForTimeout(100)
const head=await grid.locator('.tm-grid-head').boundingBox();check('header stays stuck while scrolling',Math.abs(head.y)<2,`y=${head.y}`)
const vis=await grid.locator('.tm-grid-body [role=row]').first().boundingBox();check('rows render at the scroll position',vis.y<900 && vis.y+vis.height>-900,`first y=${Math.round(vis.y)}`)
const dates=await grid.locator('.tm-grid-body [role=row] [role=cell]:first-child').allInnerTexts();check('dates are human',dates.every(d => /^\d{1,2} [A-Z][a-z]{2} \d{4}$/.test(d)),dates[0])
check('newest first by default',await grid.locator('.tm-grid-head [role=columnheader]').first().getAttribute('aria-sort')==='descending')
await page.evaluate(() => {const g=document.querySelector('[aria-label="Entries"]');scrollTo(0,g.getBoundingClientRect().top+scrollY-40)});await settle()
await grid.getByRole('button',{name:'Payee'}).click();await settle();const pay=await grid.locator('.tm-grid-body [role=row] [role=cell]:nth-child(2)').allInnerTexts();check('sort by column',pay.slice(0,5).join()===[...pay.slice(0,5)].sort((a,b) => a.localeCompare(b)).join(),pay.slice(0,2).join())
const amountAlign=await grid.locator('.tm-grid-body [role=row] [role=cell]:nth-child(5)').first().evaluate(c => getComputedStyle(c).textAlign),textAlign=await grid.locator('.tm-grid-body [role=row] [role=cell]:nth-child(2)').first().evaluate(c => getComputedStyle(c).textAlign);check('alignment follows type',amountAlign==='right' && textAlign==='start',`${amountAlign}/${textAlign}`)
check('machine fields stay out of the grid',!(await grid.innerText()).includes('expenses:'))
const row=grid.locator('.tm-grid-body [role=row][tabindex="0"]');await row.focus();const r0=await row.getAttribute('data-row');await page.keyboard.press('ArrowDown');await settle();const r1=await page.evaluate(() => document.activeElement?.getAttribute('data-row'));check('arrow keys move between rows',Number(r1)===Number(r0)+1,`${r0} to ${r1}`)
await page.keyboard.press('Enter');await page.getByRole('dialog').waitFor();check('Enter opens Details with machine fields',(await page.getByRole('dialog').innerText()).includes('Entry'));await page.keyboard.press('Escape');await page.waitForTimeout(250)
const cats=page.getByRole('table',{name:'Categories'});check('total only on declared columns',(await cats.locator('.tm-grid-total').innerText()).includes('€') && (await cats.locator('.tm-grid-total [role=cell]').nth(4).innerText())==='',(await cats.locator('.tm-grid-total').innerText()).replace(/\n/g,' '))
check('entries grid has no total row',await grid.locator('.tm-grid-total').count()===0)

// Stat stability.
const stats=page.locator('[data-testid=stat-pair] .tm-stat');const hs=await stats.evaluateAll(all => all.map(e => e.firstElementChild.parentElement.children.length && [...e.children].slice(0,4).map(c => c.getBoundingClientRect().height).join()));check('skeleton and loaded stat have the same line heights',hs[0]===hs[1],`${hs[0]} vs ${hs[1]}`)
check('lower cost is green, higher is red',await page.locator('[data-testid=stat-pair] .tm-stat').nth(1).locator('.good').count()===1 && await page.locator('[data-testid=stat-pair] .tm-stat').nth(2).locator('.bad').count()===1)

// RankedBars.
const small=page.getByRole('button',{name:/small payees/});check('small rows collapse with a count',await small.count()===1,await small.innerText());await small.click();check('small rows expand',await page.getByRole('button',{name:/small payees/}).count()===0)

// Legacy adapter.
const bars=page.locator('.dashboard-chart').nth(1).locator('.u-over');await bars.scrollIntoViewIfNeeded();const bb=await bars.boundingBox();await page.mouse.click(bb.x+bb.width*.5,bb.y+bb.height*.7);await settle();check('legacy drilldown goes to the journal',await page.evaluate(() => window.__router.state.location.pathname)==='/journal',await page.evaluate(() => window.__router.state.location.href))
await page.locator('.dashboard-chart').filter({hasText:'Everyday'}).first().locator('.tm-legend').getByRole('button',{name:'Everyday'}).click();await settle();check('legacy donut legend hides a slice',await page.locator('.dashboard-chart').filter({hasText:'Everyday'}).first().locator('svg circle').count()===3)
const ranked=page.getByRole('list',{name:'Top categories'});await ranked.getByRole('button',{name:/See entries for Rent/}).count();check('legacy categorical bar becomes ranked bars',await ranked.count()===1)

// Resize and theme.
await open();const w0=await page.locator('canvas').first().evaluate(c => c.getBoundingClientRect().width);await page.setViewportSize({width:1000,height:800});await page.waitForTimeout(250);const w1=await page.locator('canvas').first().evaluate(c => c.getBoundingClientRect().width);check('chart follows its container',w1<w0 && w1>300,`${w0} to ${w1}`);await page.setViewportSize({width:1440,height:900});await page.waitForTimeout(200)
const pixels=() => page.locator('canvas').first().evaluate(c => {const x=c.getContext('2d'),d=x.getImageData(0,0,c.width,c.height).data;let sum=0;for(let i=0;i<d.length;i+=4000)sum+=d[i]+d[i+1]+d[i+2];return sum})
const light=await pixels();await page.evaluate(() => document.documentElement.classList.add('dark'));await page.waitForTimeout(250);const dark=await pixels();check('canvas redraws for dark theme',light!==dark);const stroke=await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--c1').trim());check('amber token follows the theme',stroke==='#b88a10',stroke);await page.evaluate(() => document.documentElement.classList.remove('dark'));await page.waitForTimeout(150);check('amber is #a87700 in light',await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--c1').trim())==='#a87700')

// IndexedDB.
const idb=await page.evaluate(async () => {const d=window.__demo;const saved=await d.writeCached({scope:'demo',revision:5,etag:'"r5"',wire:{...d.wire,flows:{d:[],a:[],p:[],t:[],v:[],n:[]}}});const stale=await d.writeCached({scope:'demo',revision:4,etag:'"r4"',wire:d.wire});const got=await d.readCached('demo');const other=await d.readCached('someone-else');return {saved,stale,revision:got?.revision,etag:got?.etag,other}})
check('cube persists by revision and refuses an older one',idb.saved && !idb.stale && idb.revision===5 && idb.etag==='"r5"',JSON.stringify(idb));check('cache is per user',idb.other===null)

// Focus visibility on every kind of control.
await open();const kinds=['.tm-btn','.tm-pill .o','.tm-chip','.tm-legend button','.tm-ranked button','.tm-moved button','.tm-grid-head button','.tm-plot','.tm-grip','.tm-nav-win','.tm-details-link']
await page.locator('.tm-chip').first().click();await settle()
for(const sel of kinds){const el=page.locator(sel).first();await page.keyboard.press('Tab');await el.focus();const v=await el.evaluate(e => {const s=getComputedStyle(e);return {style:s.outlineStyle,width:parseFloat(s.outlineWidth),match:e.matches(':focus-visible')}});check(`visible focus on ${sel}`,v.style!=='none' && v.width>=2,JSON.stringify(v))}
await context.close()

// Phone: pinch and layout.
const phone=await browser.newContext({viewport:{width:390,height:844},hasTouch:true,isMobile:true,deviceScaleFactor:2,reducedMotion:'reduce'})
const p=await phone.newPage();p.on('pageerror',e => errors.push(e.message))
await p.goto(`${base}?theme=light`);await p.locator('.u-over').first().waitFor();await p.waitForTimeout(300)
const pdays=async () => Number((await p.locator('.tm-time-status').innerText()).match(/([\d,]+) days?/)[1].replace(',',''))
check('no horizontal overflow at 390',await p.evaluate(() => document.documentElement.scrollWidth<=document.documentElement.clientWidth),await p.evaluate(() => `${document.documentElement.scrollWidth}/${document.documentElement.clientWidth}`))
check('plots keep vertical swipes as page scroll',await p.locator('.tm-plot').first().evaluate(e => getComputedStyle(e).touchAction)==='pan-y')
await p.locator('.u-over').first().scrollIntoViewIfNeeded();const pb=await p.locator('.u-over').first().boundingBox(),cdp=await phone.newCDPSession(p),cy=pb.y+60,cx=pb.x+pb.width/2
const touch=(type,a,b) => cdp.send('Input.dispatchTouchEvent',{type,touchPoints:type==='touchEnd' ? [] : [{x:a,y:cy,id:1},{x:b,y:cy,id:2}]})
await touch('touchStart',cx-30,cx+30);for(let i=1;i<=8;i++){await touch('touchMove',cx-30-i*8,cx+30+i*8);await p.waitForTimeout(30)}await touch('touchEnd');await p.waitForTimeout(200)
const pinched=await pdays();check('pinch zooms',pinched<365,`${pinched} days`)
check('reduced motion stops the skeleton',await p.locator('.tm-skel').first().evaluate(e => getComputedStyle(e).animationName==='none' || parseFloat(getComputedStyle(e).animationDuration)<.01))
check('phone grid drops the header for a sort control',await p.getByRole('table',{name:'Entries'}).locator('.tm-grid-sort select').isVisible())
await p.locator('.tm-pill').count();const tapTargets=await p.locator('.tm-presets .tm-btn').evaluateAll(all => Math.min(...all.map(e => e.getBoundingClientRect().height)));check('presets are big enough to tap',tapTargets>=34,`${tapTargets}px`)
await p.keyboard.press('Control+k');const pc=p.getByRole('dialog');await pc.waitFor();await p.waitForTimeout(250);const pcb=await pc.boundingBox();check('phone command bar fits',pcb.x>=0 && pcb.x+pcb.width<=390,JSON.stringify(pcb));await p.keyboard.press('Escape');await p.waitForTimeout(400)
await p.evaluate(() => scrollTo(0,0));await p.getByRole('button',{name:'How was Money out calculated?'}).click();const pd=p.getByRole('dialog');await pd.waitFor();await p.waitForTimeout(250);const pdb=await pd.boundingBox();check('phone drawer fills the width',pdb.x>=0 && pdb.width>=380,JSON.stringify(pdb));await p.keyboard.press('Escape');await p.waitForTimeout(400)
const prow=p.getByRole('table',{name:'Entries'}).locator('.tm-grid-body [role=row]').first();const rt=await prow.boundingBox(),title=await prow.locator('[data-m=title]').boundingBox(),value=await prow.locator('[data-m=value]').boundingBox(),meta=await prow.locator('[data-m=meta]').first().boundingBox();check('phone row leads with title and amount',Math.abs(title.y-value.y)<3 && value.x>title.x && meta.y>title.y && rt.width<=390,JSON.stringify({title,value,meta}))
await phone.close()
check('no page errors',errors.length===0,errors.slice(0,3).join(' | '))
await browser.close()
console.log(`${passed} passed, ${failed} failed`)
process.exit(failed ? 1 : 0)
