import assert from 'node:assert/strict';
import {chromium} from 'playwright';
import {smokeSettings,signInSmoke,signOutSmoke} from './smoke-auth.mjs';
let settings;try{settings=smokeSettings();}catch{console.error('Invalid persistent eval smoke configuration (credentials withheld)');process.exit(1);}
const browser=await chromium.launch({...(process.env.CHROMIUM?{executablePath:process.env.CHROMIUM}:{}),args:['--no-sandbox']});
const context=await browser.newContext(),page=await context.newPage(),errors=[];page.setDefaultTimeout(15000);page.on('pageerror',error=>errors.push(error.message));
let identityEnabled,failure,phase='health';
try {
 const health=await context.request.get(settings.origin+'/healthz',{timeout:10000});assert.equal(health.status(),200,'App health unavailable');
 if(process.env.MONEYBAG_SMOKE_VERSION)assert.equal((await health.json()).version,process.env.MONEYBAG_SMOKE_VERSION,'Unexpected deployed release');
 phase='authentication';await page.goto(settings.origin+'/login?'+new URLSearchParams({next:'/settings'}));
 identityEnabled=await page.getByRole('button',{name:'Continue with Microapps ID',exact:true}).count()===1;
 const session=await signInSmoke(page,settings,identityEnabled);
 phase='account reads';
 for(const path of ['/api/bootstrap','/api/settings','/api/saved-views']) {
  const response=await context.request.get(settings.origin+path,{timeout:10000});assert.equal(response.status(),200,'Eval account read failed');const data=await response.json();
  if(path==='/api/bootstrap'){assert.equal(data.session.user.id,session.user.id);assert.equal(data.session.user.email.toLowerCase(),settings.email);assert.equal(Boolean(data.session.identity_enabled),identityEnabled);assert(Array.isArray(data.dashboards) && Array.isArray(data.tables));}
  if(path==='/api/settings')assert.equal(typeof data.is_admin,'boolean');
  if(path==='/api/saved-views')assert(data.views===null || Array.isArray(data.views));
 }
 phase='account controls';
 assert.equal(await page.getByText('Change password',{exact:true}).count(),identityEnabled?0:2);
 const manage=page.getByRole('link',{name:'Manage your account in Microapps ID',exact:true});assert.equal(await manage.count(),identityEnabled?1:0);if(identityEnabled)assert.equal(await manage.getAttribute('href'),settings.identityOrigin+'/account');
 const logout=page.getByRole('button',{name:'Sign out',exact:true});assert.equal(await logout.evaluate(el=>el.closest('form').getAttribute('action')),identityEnabled?'/api/auth/id/logout':'/logout');
 phase='UI signout';await logout.click();await page.waitForURL(u=>u.origin===settings.origin && ['/','/login'].includes(u.pathname));
 assert.equal((await context.request.get(settings.origin+'/api/session',{timeout:10000})).status(),401,'UI signout left app session');
 if(identityEnabled)assert.equal((await context.request.get(settings.identityOrigin+'/account',{maxRedirects:0,timeout:10000})).status(),200,'UI logout removed global Microapps ID device');
 assert.deepEqual(errors,[]);
}catch{failure=new Error(`Persistent eval smoke failed during ${phase} (details withheld)`);}
finally {
 if(identityEnabled!==undefined)try{await signOutSmoke(page,settings,identityEnabled);}catch(error){failure=failure?new Error(`${failure.message}; ${error.message}`):error;}
 await context.close();await browser.close();
}
if(failure){console.error(failure.message);process.exitCode=1;}else console.log(`Persistent eval authentication, account reads, UI signout and own-session cleanup passed (${identityEnabled?'Microapps ID':'legacy'})`);
