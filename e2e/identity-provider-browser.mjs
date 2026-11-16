import assert from 'node:assert/strict';
import {createHash,randomBytes} from 'node:crypto';
import {lstat,readFile} from 'node:fs/promises';
import {chromium} from 'playwright';

async function privateFile(path) {
 const info=await lstat(path);
 assert(info.isFile() && (info.mode&0o777)===0o600 && info.size<=4096,'private owned fixture required');
 return readFile(path,'utf8');
}
const cfg=JSON.parse(await privateFile(process.env.MONEYBAG_ID_BROWSER_FIXTURE));
for(const origin of [cfg.origin,cfg.issuer]) {const u=new URL(origin);assert(u.protocol==='http:' && ['localhost','127.0.0.1'].includes(u.hostname),'owned loopback origins required');}
assert(/^eval-[^@]+@example\.test$/.test(cfg.email));
const password=(await privateFile(cfg.passwordFile)).replace(/\n$/,'');
const browser=await chromium.launch({executablePath:process.env.CHROMIUM,args:['--no-sandbox']});
const context=await browser.newContext();const page=await context.newPage();const errors=[];
page.on('pageerror',error=>errors.push(error.message));page.setDefaultTimeout(10000);
try {
 let oauth;
 let destination=cfg.invitation?`/register?${new URLSearchParams({token:cfg.invitation})}`:`/login?${new URLSearchParams({next:'/settings?identity_eval=1'})}`;
 if(cfg.stage==='oauth') {
  const redirect='http://127.0.0.1:8484/owned-callback';
  await page.route(redirect+'**',route=>route.fulfill({contentType:'text/html',body:'<h1>Owned client callback</h1>'}));
  const registered=await context.request.post(cfg.origin+'/oauth/register',{data:{client_name:'Owned OAuth client',redirect_uris:[redirect],token_endpoint_auth_method:'none'}});
  assert.equal(registered.status(),201);const {client_id:clientID}=await registered.json();
  const verifier=randomBytes(32).toString('base64url'),state=randomBytes(24).toString('base64url');
  destination='/oauth/authorize?'+new URLSearchParams({client_id:clientID,redirect_uri:redirect,response_type:'code',scope:'ledger:read',state,code_challenge:createHash('sha256').update(verifier).digest('base64url'),code_challenge_method:'S256',resource:cfg.origin+'/mcp'});
  oauth={redirect,clientID,verifier,state};
 }
 await page.goto(cfg.origin+destination);
 let callback;
 if(cfg.stage==='rollback') {
  await page.getByLabel('Email',{exact:true}).fill(cfg.email);await page.getByLabel('Password',{exact:true}).fill(password);await page.getByRole('button',{name:'Sign in',exact:true}).click();
 } else {
  await page.getByRole('button',{name:'Continue with ID',exact:true}).click();
  try { await page.getByLabel('Email',{exact:true}).waitFor(); } catch { await page.screenshot({path:'/tmp/moneybag-provider-failure.png'});throw new Error(`ID sign-in missing at ${new URL(page.url()).origin}${new URL(page.url()).pathname}: ${(await page.locator('body').innerText()).slice(0,500)}`); }assert.equal(new URL(page.url()).origin,cfg.issuer);
  await page.getByLabel('Email',{exact:true}).fill(cfg.email);await page.getByRole('button',{name:'Next',exact:true}).click();
  await page.getByLabel('Password',{exact:true}).waitFor();assert.equal(new URL(page.url()).origin,cfg.issuer);
  await page.getByLabel('Password',{exact:true}).fill(password);
  const returned=page.waitForResponse(response=>new URL(response.url()).origin===cfg.origin && new URL(response.url()).pathname==='/api/auth/id/callback');
  await page.getByRole('button',{name:'Sign in',exact:true}).click();callback=await returned;
 }
 let agentToken;
 async function mcp(name,args,token) {
  const response=await context.request.post(cfg.origin+'/mcp',{headers:{Authorization:`Bearer ${token}`,'Content-Type':'application/json',Accept:'application/json, text/event-stream','MCP-Protocol-Version':'2026-07-28','Mcp-Method':'tools/call','Mcp-Name':name},data:{jsonrpc:'2.0',id:1,method:'tools/call',params:{name,arguments:args,_meta:{'io.modelcontextprotocol/protocolVersion':'2026-07-28','io.modelcontextprotocol/clientInfo':{name:'moneybag-owned-test',version:'1'},'io.modelcontextprotocol/clientCapabilities':{}}}}});
  return response;
 }
 if(cfg.stage==='uninvited') {
  assert.equal(callback.status(),403);assert.equal((await context.request.get(cfg.origin+'/api/session')).status(),401);
  assert(!(await context.cookies(cfg.origin)).some(c=>c.name==='__Host-moneybag_session'));
 } else {
  if(callback)assert.equal(callback.status(),303,'provider callback must succeed');
  if(oauth)await page.getByRole('button',{name:'Allow access',exact:true}).waitFor();
  else {await page.waitForURL(u=>u.origin===cfg.origin && u.pathname!=='/login' && u.pathname!=='/register');if(!cfg.invitation)assert.equal(new URL(page.url()).searchParams.get('identity_eval'),'1');}
  const me=await context.request.get(cfg.origin+'/api/session');assert.equal(me.status(),200);const session=await me.json();
  assert.equal(session.user.email,cfg.email);assert.equal(session.user.is_admin,false);
  if(cfg.userID)assert.equal(session.user.id,cfg.userID);
  if(cfg.email==='eval-browser@example.test')assert.equal(session.user.name,'Keep local name');
  const enabled=cfg.stage!=='rollback';assert.equal(Boolean(session.identity_enabled),enabled);
  const cookies=await context.cookies(cfg.origin);const cookie=cookies.find(c=>c.name===(enabled?'__Host-moneybag_session':'moneybag_session'));
  assert(cookie && cookie.httpOnly && cookie.sameSite==='Lax');if(enabled)assert(cookie.secure);
  assert(!cookies.some(c=>['__Host-idclient-flow','__Host-moneybag-id-invitation',enabled?'moneybag_session':'__Host-moneybag_session'].includes(c.name)));
  if(enabled) {
   assert.equal((await context.request.get(callback.url(),{maxRedirects:0})).status(),403);
   for(const path of ['/login','/setup','/register','/settings/password'])assert.equal((await context.request.post(cfg.origin+path,{headers:{Origin:cfg.origin},form:{csrf:session.csrf}})).status(),403);
   assert.equal((await context.request.post(cfg.origin+'/api/auth/id/logout',{headers:{Origin:'https://wrong.example.test'},maxRedirects:0})).status(),403);
   assert.equal((await context.request.get(cfg.origin+'/api/session')).status(),200);
  }
  const invite=await context.request.post(cfg.origin+'/settings/invite',{headers:{Origin:cfg.origin,Accept:'application/json'},form:{csrf:session.csrf,email:'eval-must-refuse@example.test'}});
  assert.equal(invite.status(),400,'local non-admin must not issue invitations');
  if(oauth) {
   await page.getByRole('button',{name:'Allow access',exact:true}).click();await page.waitForURL(oauth.redirect+'**');
   const returned=new URL(page.url());assert.equal(returned.searchParams.get('state'),oauth.state);assert(returned.searchParams.get('code'));
   const token=await context.request.post(cfg.origin+'/oauth/token',{form:{grant_type:'authorization_code',client_id:oauth.clientID,code:returned.searchParams.get('code'),redirect_uri:oauth.redirect,code_verifier:oauth.verifier,resource:cfg.origin+'/mcp'}});
   assert.equal(token.status(),200);agentToken=(await token.json()).access_token;assert(agentToken);
   const who=await mcp('whoami',{},agentToken);assert.equal(who.status(),200);const result=(await who.json()).result;assert(!result.isError);assert.equal(result.structuredContent.user.id,cfg.userID);assert.equal(result.structuredContent.user.is_admin,false);assert.equal(result.structuredContent.actor.kind,'agent');
   const write=await mcp('account_create',{id:'assets:must-refuse',name:'Must refuse'},agentToken);assert.equal(write.status(),200);const refusal=await write.json();assert(refusal.error || refusal.result?.isError,'read-only agent wrote data');
  }
  await page.goto(cfg.origin+'/settings');await page.getByRole('heading',{name:'Account',exact:true}).waitFor();
  assert.equal(await page.getByText('Change password',{exact:true}).count(),enabled?0:2);
  const manage=page.getByRole('link',{name:'Manage your account in ID',exact:true});assert.equal(await manage.count(),enabled?1:0);if(enabled)assert.equal(await manage.getAttribute('href'),cfg.issuer+'/account');
  await page.getByRole('button',{name:'Sign out',exact:true}).click();await page.waitForURL(u=>u.origin===cfg.origin && ['/','/login'].includes(u.pathname));
  assert.equal((await context.request.get(cfg.origin+'/api/session')).status(),401);
  if(agentToken) {
   assert.equal((await mcp('whoami',{},agentToken)).status(),200,'browser logout revoked agent grant');
   assert.equal((await context.request.post(cfg.origin+'/oauth/revoke',{form:{client_id:oauth.clientID,token:agentToken}})).status(),200);
   assert.equal((await mcp('whoami',{},agentToken)).status(),401);
  }
 }
 if(cfg.stage!=='rollback') {
  const account=await context.request.get(cfg.issuer+'/account',{maxRedirects:0});assert.equal(account.status(),200,'app logout removed global ID device');
  const csrf=(await account.text()).match(/name="csrf" value="([A-Za-z0-9_-]+)"/)?.[1];assert(csrf);
  assert.equal((await context.request.post(cfg.issuer+'/signout',{headers:{Origin:cfg.issuer},form:{csrf},maxRedirects:0})).status(),303);
 }
 assert.deepEqual(errors,[]);console.log(`Owned ${cfg.stage} authentication and session cleanup passed`);
}finally{await context.close();await browser.close()}
