import assert from "node:assert/strict";
import { createServer } from "node:http";
import { createHash, randomBytes } from "node:crypto";
import { readFile, readdir, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { spawn } from "node:child_process";

// Loaded by Identity's owned e2e runner; never use a production issuer or account.
export async function lifecycle({browser, base, mail, setupCode, root}) {
  assert(["localhost","127.0.0.1"].includes(new URL(base).hostname));
  assert(process.env.MONEYBAG_TEST_DATABASE_URL, "explicit owned PostgreSQL server required");
  const owner=await browser.newContext();
  const member=await browser.newContext();
  const admin=await owner.newPage();
  const person=await member.newPage();
  const errors=[];
  for (const context of [owner,member]) context.on("page",page=>page.on("pageerror",e=>errors.push(e.message)));
  const password=randomBytes(24).toString("base64url");
  const state=randomBytes(24).toString("base64url");
  const verifier=randomBytes(32).toString("base64url");
  const challenge=createHash("sha256").update(verifier).digest("base64url");
  let code;
  const callback=createServer((request,response)=>{
    const url=new URL(request.url,"http://localhost");
    if(url.pathname==="/mcp-callback" && url.searchParams.get("state")===state && url.searchParams.has("code")) {
      code=url.searchParams.get("code");response.writeHead(200,{"content-type":"text/html"});response.end("<!doctype html><h1>Owned MCP callback</h1>");
    } else {response.writeHead(404);response.end();}
  });
  await new Promise((ok,fail)=>{callback.once("error",fail);callback.listen(0,"127.0.0.1",ok);});
  const callbackURL=`http://127.0.0.1:${callback.address().port}/mcp-callback`;
  // Reserve the app port while registering its exact callback, release immediately before Go binds it.
  const reservation=createServer();
  await new Promise((ok,fail)=>{reservation.once("error",fail);reservation.listen(0,"127.0.0.1",ok);});
  const appPort=reservation.address().port;
  const appOrigin=`http://localhost:${appPort}`;
  let child;
  try {
    await admin.goto(`${base}/setup`);
    await admin.locator("[name=code]").fill(setupCode);
    await admin.getByRole("button",{name:"Continue",exact:true}).click();
    await admin.locator("[name=name]").fill("Eval Owner");
    await admin.locator("[name=email]").fill("eval-owner@example.test");
    await admin.locator("[name=password]").fill(password);
    await admin.locator("[name=passkey]").uncheck();
    await admin.getByRole("button",{name:"Create account",exact:true}).click();
    await admin.waitForURL(`${base}/welcome`);
    const registration=await owner.request.post(`${base}/register`,{data:{client_name:"Moneybag owned test",redirect_uris:[callbackURL],token_endpoint_auth_method:"none"}});
    assert.equal(registration.status(),201);
    const mcpID=(await registration.json()).client_id;
    const query=new URLSearchParams({client_id:mcpID,redirect_uri:callbackURL,response_type:"code",scope:"mcp:admin",state,resource:`${base}/mcp`,code_challenge:challenge,code_challenge_method:"S256"});
    await admin.goto(`${base}/authorize?${query}`);
    await admin.locator("button[name=decision][value=continue]").click();
    await admin.getByRole("button",{name:"Allow",exact:true}).click();
    await admin.waitForURL(`${callbackURL}**`);
    assert(code,"MCP authorization code missing");
    const tokenResponse=await owner.request.post(`${base}/token`,{form:{client_id:mcpID,grant_type:"authorization_code",code,redirect_uri:callbackURL,code_verifier:verifier,resource:`${base}/mcp`}});
    assert.equal(tokenResponse.status(),200);
    const {access_token:accessToken}=await tokenResponse.json();
    assert(accessToken);
    const headers={authorization:`Bearer ${accessToken}`,accept:"application/json, text/event-stream","mcp-protocol-version":"2025-11-25"};
    let requestID=0;
    async function rpc(method,params,notification=false) {
      const id=notification?undefined:++requestID;
      const response=await owner.request.post(`${base}/mcp`,{headers,data:{jsonrpc:"2.0",...(notification?{}:{id}),method,params},timeout:15_000});
      if(notification){assert([200,202,204].includes(response.status()),"MCP notification rejected");return;}
      assert.equal(response.status(),200,`MCP ${method} failed`);
      const session=response.headers()["mcp-session-id"];if(session)headers["mcp-session-id"]=session;
      const text=await response.text();
      let message;
      try {
        if(response.headers()["content-type"].includes("application/json"))message=JSON.parse(text);
        else message=text.split("\n").filter(line=>line.startsWith("data:")).map(line=>JSON.parse(line.slice(5))).find(value=>value.id===id);
      } catch {throw new Error("Invalid MCP fixture response (body withheld)");}
      assert(message?.result && !message.error,`MCP ${method} returned an error`);
      return message.result;
    }
    await rpc("initialize",{protocolVersion:"2025-11-25",capabilities:{},clientInfo:{name:"moneybag-owned-test",version:"1"}});
    await rpc("notifications/initialized",{},true);
    async function tool(name,args) {
      const result=await rpc("tools/call",{name,arguments:args});
      assert(!result.isError,`MCP ${name} failed`);
      assert(result.structuredContent,`MCP ${name} has no structured result`);
      return result.structuredContent;
    }
    const created=await tool("app_create",{kind:"confidential",settings:{name:"Eval Moneybag",home_url:appOrigin,redirect_uris:[`${appOrigin}/api/auth/id/callback`],trusted:true,refresh:false}});
    assert(created.app.id && created.client_secret);
    for (const email of ["eval-browser@example.test", "eval-new@example.test"]) {
      const invited=await tool("invite_create",{email,max_uses:1,valid_hours:72});
      assert(invited.email_sent);
      let invite;
      const deadline=Date.now()+5_000;
      while(!invite && Date.now()<deadline) {
        for(const name of await readdir(mail)) {
          const message=JSON.parse(await readFile(join(mail,name),"utf8"));
          if(message.to.includes(email))invite=message.text.split("\n").find(line=>line.startsWith(`${base}/join/`));
        }
        if(!invite)await new Promise(ok=>setTimeout(ok,50));
      }
      assert(invite,"captured eval invitation missing");
      await person.goto(invite);
      await person.locator("[name=name]").fill("Eval browser");
      await person.locator("[name=password]").fill(password);
      await person.locator("[name=passkey]").uncheck();
      await person.getByRole("button",{name:"Create account",exact:true}).click();
      await person.waitForURL(`${base}/joined`);
      await person.goto(`${base}/account`);
      const csrf=await person.locator('form[action="/signout"] [name=csrf]').inputValue();
      assert.equal((await member.request.post(`${base}/signout`,{form:{csrf},maxRedirects:0})).status(),303);
    }
    const secretFile=join(root,"moneybag-client-secret");
    const passwordFile=join(root,"moneybag-eval-password");
    await writeFile(secretFile,created.client_secret,{mode:0o600});
    await writeFile(passwordFile,password,{mode:0o600});
    await new Promise((ok,fail)=>reservation.close(error=>error?fail(error):ok()));
    const moneybag=resolve(import.meta.dirname,"..");
    child=spawn("go",["test","-race","-timeout","3m","./integration","-run","TestIdentityActualProviderBrowser","-v"],{cwd:join(moneybag,"backend"),stdio:["ignore","pipe","pipe"],env:{...process.env,MONEYBAG_ID_TEST_ISSUER:base,MONEYBAG_ID_TEST_CLIENT_ID:created.app.id,MONEYBAG_ID_TEST_SECRET_FILE:secretFile,MONEYBAG_ID_TEST_PASSWORD_FILE:passwordFile,MONEYBAG_ID_TEST_APP_PORT:String(appPort)}});
    let output="";
    for(const stream of [child.stdout,child.stderr])stream.on("data",data=>{output=(output+data).slice(-16_384);});
    const result=await new Promise((ok,fail)=>{child.once("error",fail);child.once("exit",ok);});
    process.stdout.write(output);
    assert.equal(result,0,"Moneybag against real Identity failed");
    const people=await tool("people_list",{});
    for (const email of ["eval-browser@example.test", "eval-new@example.test"]) {
      const evalUser=people.people.find(user=>user.email===email);
      assert(evalUser && evalUser.devices===0,"eval browser left an ID device session behind");
    }
    const app=await tool("app_get",{id:created.app.id});
    assert(app.last_signin_at && !app.disabled && app.trusted);
    const activity=await tool("activity_tail",{kind:"mcp"});
    assert(activity.events.some(event=>event.text.includes("completed app_create")));
    assert(activity.events.some(event=>event.text.includes("completed invite_create")));
    assert.deepEqual(errors,[]);
    console.log("Moneybag real Identity: MCP registration/invite, captured email proof, signed OIDC, existing/new/invited/uninvited browser flows, Moneybag OAuth PKCE/read-only/revocation, preserved finance/model data, rollback and zero leftover ID devices passed");
  } finally {
    if(child && child.exitCode===null && child.signalCode===null)child.kill("SIGTERM");
    if(reservation.listening)await new Promise(ok=>reservation.close(ok));
    await new Promise(ok=>callback.close(ok));
    await owner.close();await member.close();
  }
}
