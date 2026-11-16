import assert from 'node:assert/strict';
import {constants,openSync,fstatSync,readFileSync,closeSync} from 'node:fs';
import {pathToFileURL} from 'node:url';

function origin(value) {
 let url;try {url=new URL(value);}catch {throw new Error('Invalid smoke origin');}
 assert(url.protocol==='https:' || (url.protocol==='http:' && ['localhost','127.0.0.1','[::1]'].includes(url.hostname)),'Smoke origins require HTTPS or exact loopback HTTP');
 assert(!url.username && !url.password && url.pathname==='/' && !url.search && !url.hash,'Smoke origins must not contain credentials, paths, queries or fragments');
 return url.origin;
}
export function smokeSettings(env=process.env) {
 const email=env.MONEYBAG_SMOKE_EMAIL ?? 'eval-smoke@microapps.space';
 assert(/^eval-[^@\s]+@[^@\s]+$/i.test(email),'Smoke requires a pre-existing eval- account');
 assert(env.MONEYBAG_SMOKE_PASSWORD_FILE,'MONEYBAG_SMOKE_PASSWORD_FILE must name a private password file');
 const fd=openSync(env.MONEYBAG_SMOKE_PASSWORD_FILE,constants.O_RDONLY|constants.O_NOFOLLOW|constants.O_NONBLOCK);
 let password;try {
  const info=fstatSync(fd);assert(info.isFile() && (info.mode&0o777)===0o600 && info.size>0 && info.size<=514,'Smoke password must be a nonempty private regular file');
  password=readFileSync(fd,'utf8').replace(/\r?\n$/,'');assert(password.length>0 && Buffer.byteLength(password)<=512 && !/[\r\n]/.test(password),'Invalid smoke password file');
 }finally{closeSync(fd);}
 return {email:email.toLowerCase(),password,origin:origin(env.MONEYBAG_SMOKE_ORIGIN ?? 'https://moneybag.microapps.space'),identityOrigin:origin(env.MONEYBAG_SMOKE_ID_ORIGIN ?? 'https://id.microapps.space')};
}
async function fillPassword(page,password) {
 try{await page.getByLabel('Password',{exact:true}).fill(password);}catch{throw new Error('Could not fill smoke password (details withheld)');}
}
// No account creation fallback and no financial writes.
export async function signInSmoke(page,settings,identityEnabled) {
 if(identityEnabled) {
  await page.getByRole('button',{name:'Continue with Microapps ID',exact:true}).click();
  await page.getByLabel('Email',{exact:true}).waitFor();assert.equal(new URL(page.url()).origin,settings.identityOrigin,'Unexpected Microapps ID email origin');
  await page.getByLabel('Email',{exact:true}).fill(settings.email);await page.getByRole('button',{name:'Next',exact:true}).click();
  await page.getByLabel('Password',{exact:true}).waitFor();assert.equal(new URL(page.url()).origin,settings.identityOrigin,'Unexpected password entry origin');
  await fillPassword(page,settings.password);await page.getByRole('button',{name:'Sign in',exact:true}).click();
 }else {
  assert.equal(new URL(page.url()).origin,settings.origin,'Unexpected app login origin');
  await page.getByLabel('Email',{exact:true}).fill(settings.email);await fillPassword(page,settings.password);await page.getByRole('button',{name:'Sign in',exact:true}).click();
 }
 await page.waitForURL(u=>u.origin===settings.origin && u.pathname==='/settings');
 await page.getByRole('heading',{name:'Account',exact:true}).waitFor();
 const me=await page.context().request.get(settings.origin+'/api/session',{timeout:10000});assert.equal(me.status(),200,'Smoke app session missing');
 const session=await me.json();assert.equal(session.user.email.toLowerCase(),settings.email,'Wrong smoke account');assert.equal(Boolean(session.identity_enabled),identityEnabled,'Wrong authentication mode');
 return session;
}
export async function signOutSmoke(page,settings,identityEnabled) {
 const request=page.context().request,failures=[];
 try {
  const session=await request.get(settings.origin+'/api/session',{timeout:10000});
  if(identityEnabled || session.status()===200) {
   const csrf=session.status()===200?(await session.json()).csrf:'';
   assert.equal((await request.post(settings.origin+(identityEnabled?'/api/auth/id/logout':'/logout'),{headers:{Origin:settings.origin},form:{csrf},maxRedirects:0,timeout:10000})).status(),303,'Own app signout failed');
  }else assert.equal(session.status(),401,'App cleanup unavailable');
  assert.equal((await request.get(settings.origin+'/api/session',{timeout:10000})).status(),401,'App smoke session remained active');
 }catch{failures.push('App smoke session cleanup failed');}
 if(identityEnabled)try {
  const account=await request.get(settings.identityOrigin+'/account',{maxRedirects:0,timeout:10000});
  if(account.status()===200) {
   const csrf=(await account.text()).match(/name="csrf" value="([A-Za-z0-9_-]+)"/)?.[1];assert(csrf,'Own Microapps ID device signout missing');
   assert.equal((await request.post(settings.identityOrigin+'/signout',{headers:{Origin:settings.identityOrigin},form:{csrf},maxRedirects:0,timeout:10000})).status(),303,'Own Microapps ID device signout failed');
  }else assert([302,303,401].includes(account.status()),'Own Microapps ID cleanup unavailable');
 }catch{failures.push('Own Microapps ID device cleanup failed');}
 assert.equal(failures.length,0,failures.join('; '));
}
if(process.argv[1] && import.meta.url===pathToFileURL(process.argv[1]).href && process.argv[2]==='--check-config') {
 try {smokeSettings();console.log('Persistent eval smoke configuration checked (credentials withheld)');}
 catch {console.error('Invalid persistent eval smoke configuration (credentials withheld)');process.exitCode=1;}
}
