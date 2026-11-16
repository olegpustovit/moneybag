import assert from 'node:assert/strict';
import {mkdtempSync,writeFileSync,chmodSync,symlinkSync,rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {spawnSync} from 'node:child_process';
import {smokeSettings} from './smoke-auth.mjs';
const root=mkdtempSync(join(tmpdir(),'moneybag-smoke-config-'));
try {
 const file=join(root,'password');const secret='private-fixture-password';writeFileSync(file,secret+'\n',{mode:0o600});
 const env={MONEYBAG_SMOKE_PASSWORD_FILE:file};
 assert.equal(smokeSettings(env).email,'eval-smoke@microapps.space');assert.equal(smokeSettings(env).password,secret);
 assert.equal(smokeSettings({...env,MONEYBAG_SMOKE_EMAIL:'Eval-Owned@EXAMPLE.test',MONEYBAG_SMOKE_ORIGIN:'http://localhost:1234',MONEYBAG_SMOKE_ID_ORIGIN:'http://[::1]:4321'}).email,'eval-owned@example.test');
 for(const email of ['owner@example.test','eval-a b@example.test','eval-a@@example.test','eval-@example.test'])assert.throws(()=>smokeSettings({...env,MONEYBAG_SMOKE_EMAIL:email}));
 for(const origin of ['http://production.test','http://localhost.evil.test','ftp://localhost','https://user:private@example.test','https://example.test/path','https://example.test?private=1','https://example.test/#private'])for(const key of ['MONEYBAG_SMOKE_ORIGIN','MONEYBAG_SMOKE_ID_ORIGIN'])assert.throws(()=>smokeSettings({...env,[key]:origin}));
 for(const content of ['',secret+'\nsecond',secret+'\rinside','x'.repeat(515)]) {writeFileSync(file,content);assert.throws(()=>smokeSettings(env));}
 writeFileSync(file,secret+'\r\n');assert.equal(smokeSettings(env).password,secret);
 chmodSync(file,0o644);assert.throws(()=>smokeSettings(env));chmodSync(file,0o600);
 const link=join(root,'link');symlinkSync(file,link);assert.throws(()=>smokeSettings({...env,MONEYBAG_SMOKE_PASSWORD_FILE:link}));
 assert.throws(()=>smokeSettings({...env,MONEYBAG_SMOKE_PASSWORD_FILE:root}));assert.throws(()=>smokeSettings({}));
 const failure=spawnSync(process.execPath,[join(import.meta.dirname,'smoke-auth.mjs'),'--check-config'],{encoding:'utf8',env:{...process.env,...env,MONEYBAG_SMOKE_EMAIL:'owner@example.test'}});
 assert.equal(failure.status,1);assert(!failure.stdout.includes(secret) && !failure.stderr.includes(secret));
 console.log('Persistent eval smoke credential/origin preflight checks passed');
}finally{rmSync(root,{recursive:true,force:true});}
