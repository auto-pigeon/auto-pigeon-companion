import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';
import assert from 'node:assert/strict';

// Drive the shipped event handlers with controlled network completion order.
// Live browser acceptance separately exercises the real controls/server.
function harness() {
  const nodes=new Map(), requests=[], messages=[], installs=[], handoffs=[];
  class Node {
    constructor(id='') {this.id=id;this.value='';this.children=[];this.dataset={};this.disabled=false;this.listeners={};this.attrs={};const classes=new Set(); this.classList={toggle(c,on){if(on===undefined)on=!classes.has(c);on?classes.add(c):classes.delete(c);},remove(c){classes.delete(c);},contains(c){return classes.has(c);}};}
    addEventListener(type,fn){(this.listeners[type] ||= []).push(fn);}
    async fire(type){for(const fn of this.listeners[type]||[]) await fn({target:this,currentTarget:this,preventDefault(){}});}
    append(...items){this.children.push(...items);}
    replaceChildren(...items){this.children=[...items];}
    setAttribute(k,v){this.attrs[k]=v;}
    removeAttribute(k){delete this.attrs[k];}
    querySelector(){return null;}
    querySelectorAll(){return [];}
    after(){} dispatchEvent(event){(this.dispatched ||= []).push(event.type);} focus(){this.focused=true;} remove(){} scrollIntoView(){}
  }
  const $=id=>{if(!nodes.has(id))nodes.set(id,new Node(id));return nodes.get(id);};
  const el=(tag,o={})=>{const n=new Node();Object.assign(n,o.attrs||{});n.textContent=o.text||'';n.append(...o.children||[]);return n;};
  const scratch={kind:'tool',executables:[{name:'qbsp',file:'qbsp'}],actions:[{id:'compile'}]};
  let tokens=[];
  const AUCOM={$,el,areas:{},t:(x,v={})=>String(x).replace(/\{(\w+)\}/g,(m,k)=>k in v?v[k]:m),programFileName:x=>x,
    api:(path,options)=>new Promise(resolve=>requests.push({path,options,resolve})),
    setMessage:(id,text,kind)=>messages.push({id,text,kind}),record(){},badge:x=>el('span',{text:x}),
    withBusy:async(n,fn)=>{n.disabled=true;try{return await fn();}finally{n.disabled=false;}},
    scratch:{request:()=>scratch,stageTokens:()=>tokens,render(){},clear(){},fill(value){Object.assign(scratch,value);},refresh:async()=>{}},
    showArea(){},status:{},openInstalledProfile:async id=>installs.push(id)};
  const document={createTextNode:x=>x,querySelectorAll:()=>[],querySelector:()=>null,getElementById:()=>null,addEventListener(){},body:{classList:{remove(){}}}};
  $('wizard-kind').value='tool';
  for(const [id,value] of Object.entries({name:'A tool',version:'1.0.0',summary:'A compiler',publisher:'Local',license:'NOASSERTION'}))$('wizard-'+id).value=value;
  runInNewContext(readFileSync(new URL('../assets/profiles.js',import.meta.url),'utf8'),{window:{AUCOM},document,console,structuredClone,URLSearchParams,Event:class{constructor(type){this.type=type;}},navigator:{}});
  // The shipped file installs this navigation helper; no navigation is needed here.
  AUCOM.openInstalledProfile=async (id,handoff=null)=>{installs.push(id);handoffs.push(handoff);};
  const valid={valid:true,id:'local.a',kind:'tool',name:'A tool',version:'1.0.0',trust:'local',document:{name:'A tool',publisher:{name:'Local'},license:{spdx:'NOASSERTION'},executables:[],actions:[]}};
  return {$,AUCOM,requests,messages,installs,handoffs,scratch,valid,setTokens:value=>tokens=value,
    invalidate:()=>$('area-new-profile').fire('input'),
    answer:(body,ok=true)=>requests.shift().resolve({ok,body}),
    next:()=>$('wizard-next').fire('click'),
    tick:()=>new Promise(r=>setImmediate(r))};
}

let count=0;
async function test(name,fn){await fn();count++;console.log('PASS '+name);}
await test('missing identity blocks Next and focuses Name',async()=>{
 const h=harness();await h.next();h.$('wizard-name').value='';await h.next();
 assert.equal(h.requests.length,0);assert.equal(h.$('wizard-name').attrs['aria-invalid'],'true');assert.equal(h.$('wizard-name').focused,true);
});
await test('invalid composition retains programs step and disables Install',async()=>{
 const h=harness();await h.next();await h.next();const done=h.next();h.answer({valid:false,error:'Missing input'});await done;
 assert.equal(h.$('wizard-step-3').classList.contains('active'),true);assert.equal(h.$('wizard-step-4').classList.contains('active'),false);assert.equal(h.$('wizard-next').disabled,false);assert.equal(h.$('wizard-import').disabled,true);assert.match(h.messages.at(-1).text,/Missing input/);
});
await test('transport failure cannot reuse the last valid review',async()=>{
 const h=harness();await h.next();await h.next();let done=h.next();h.answer(h.valid);await done;
 await h.$('wizard-back').fire('click');done=h.next();h.answer({error:'offline'},false);await done;
 assert.equal(h.$('wizard-import').disabled,true);await h.$('wizard-import').fire('click');assert.equal(h.requests.length,0);
});
await test('composition for an earlier draft is ignored',async()=>{
 const h=harness();await h.next();await h.next();const done=h.next();h.$('wizard-name').value='Different';await h.invalidate();h.answer(h.valid);await done;
 assert.equal(h.$('wizard-import').disabled,true);assert.equal(h.$('wizard-json').value,'');
});
await test('an edited advanced document must be validated before import',async()=>{
 const h=harness();await h.next();await h.next();const done=h.next();h.answer(h.valid);await done;
 h.$('wizard-json').value='{}';await h.$('wizard-import').fire('click');assert.equal(h.requests.length,0);
});
await test('double install submits once',async()=>{
 const h=harness();await h.next();await h.next();const done=h.next();h.answer(h.valid);await done;
 const install=h.$('wizard-import').fire('click');await h.$('wizard-import').fire('click');assert.equal(h.requests.length,1);
 h.answer({id:'local.a',name:'A tool'});await install;assert.deepEqual(h.installs,['local.a']);assert.equal(h.$('wizard-import').disabled,true);
});
await test('a refused stage argument save opens the installed page with the unsaved tokens, never importing twice',async()=>{
 // NEW_360: Retry used to resend the refused tokens unchanged, with every field locked.
 const h=harness();h.setTokens([{stage:'compile',arguments:['-verbose']},{stage:'vis',arguments:['-fast']}]);await h.next();await h.next();const done=h.next();h.answer(h.valid);await done;
 const install=h.$('wizard-import').fire('click');assert.match(h.requests[0].path,/import/);h.answer({id:'local.a',name:'A tool'});await h.tick();
 assert.match(h.requests[0].path,/local\.a\/stage-arguments/);h.answer({error:'argument 1 (-verbose) is already given'},false);await install;
 assert.equal(h.requests.length,0);assert.deepEqual(h.installs,['local.a']);
 assert.deepEqual(JSON.parse(JSON.stringify(h.handoffs[0])),{unsaved:[{stage:'compile',arguments:['-verbose']},{stage:'vis',arguments:['-fast']}],problem:{stage:'compile',message:'argument 1 (-verbose) is already given'}});
 assert.notEqual(h.$('wizard-import').textContent,'Retry saving stage arguments');
 await h.$('wizard-import').fire('click');assert.equal(h.requests.length,0);
});
await test('a stage argument refused while composing returns to its stage, nothing installed',async()=>{
 const h=harness();let marked=null;h.AUCOM.scratch.stageProblem=(stage,message)=>marked={stage,message};
 h.setTokens([{stage:'compile',arguments:['-nopercent']}]);await h.next();await h.next();const done=h.next();
 assert.deepEqual(JSON.parse(JSON.stringify(h.requests[0].options.body.stage_arguments)),[{stage:'compile',arguments:['-nopercent']}]);
 h.answer({valid:false,error:'already given',problems:[{path:'stage_arguments.compile',message:'already given'}]});await done;
 assert.deepEqual(marked,{stage:'compile',message:'already given'});assert.equal(h.$('wizard-step-3').classList.contains('active'),true);
 assert.equal(h.$('wizard-import').disabled,true);assert.deepEqual(h.installs,[]);
});
await test('out-of-order template lists cannot overwrite another kind',async()=>{
 const h=harness();const first=h.AUCOM.areas['new-profile'].refresh('');await h.tick();assert.match(h.requests[0].path,/kind=tool/);
 h.$('wizard-kind').value='engine';const second=h.$('wizard-kind').fire('change');assert.equal(h.requests.length,2);
 const latest=h.requests.pop();latest.resolve({ok:true,body:{items:[]}});await second;
 h.answer({items:[{id:'old.tool',name:'old'}]});await first;assert.equal(h.$('wizard-template').children.some(x=>x.value==='old.tool'),false);
});
// NEW_506 (#11): this used to assert that an answer was DROPPED whenever any
// field changed while it was on its way — and nothing noticed that the form
// then kept the previous starting point under the new one's name with Next
// disabled for good. Choosing the Game right after a starting point was enough;
// so was a hidden tab whose fields an agent typed into. Only a NEWER choice
// makes an answer stale now; what was typed into the identity fields is kept.
async function chosen(h,items=[{id:'base',kind:'tool',name:'Base',version:'1.2.0'},{id:'other',kind:'tool',name:'Other',version:'2.0.0'}]){
 const init=h.AUCOM.areas['new-profile'].refresh('');await h.tick();h.answer({items});await init;
}
await test('a template answer is applied although a field changed while it loaded, and what was typed is kept',async()=>{
 const h=harness();await chosen(h);
 h.$('wizard-template').value='base';const selection=h.$('wizard-template').fire('change');await h.tick();
 h.$('wizard-name').value='Edited';await h.invalidate();
 h.answer({scratch:{kind:'tool',from:'base'},identity:{name:'Base',description:'What Base is',release_notes:'https://example.invalid/base/1.2.0'}});await selection;await h.tick();
 assert.equal(h.$('wizard-name').value,'Edited');assert.equal(h.scratch.from,'base');
 assert.equal(h.$('wizard-description').value,'What Base is');assert.equal(h.$('wizard-release-notes').value,'https://example.invalid/base/1.2.0');
 assert.equal(h.$('wizard-next').disabled,false);assert.match(h.$('wizard-template-note').textContent,/Filled in from Base 1\.2\.0/);
});
await test('an answer for a starting point no longer chosen is dropped, and the chosen one fills',async()=>{
 const h=harness();await chosen(h);
 h.$('wizard-template').value='base';const first=h.$('wizard-template').fire('change');await h.tick();
 h.$('wizard-template').value='other';const second=h.$('wizard-template').fire('change');await h.tick();
 assert.equal(h.requests.length,2);
 h.answer({scratch:{kind:'tool',from:'base'},identity:{name:'Base'}});await first;await h.tick();
 assert.equal(h.scratch.from,undefined);assert.equal(h.$('wizard-next').disabled,true);
 h.answer({scratch:{kind:'tool',from:'other'},identity:{name:'Other'}});await second;await h.tick();
 assert.equal(h.scratch.from,'other');assert.equal(h.$('wizard-name').value,'A tool');assert.equal(h.$('wizard-next').disabled,false);
});
await test('a starting point that cannot be read says so beside the choice, and Retry asks for the same one',async()=>{
 const h=harness();await chosen(h);
 h.$('wizard-template').value='other';const selection=h.$('wizard-template').fire('change');await h.tick();
 h.answer({error:'that profile could not be read'},false);await selection;await h.tick();
 assert.equal(h.$('wizard-template-error').hidden,false);assert.match(h.$('wizard-template-error-text').textContent,/Other could not be read/);
 assert.equal(h.$('wizard-next').disabled,true);
 const retry=h.$('wizard-template-retry').fire('click');await h.tick();
 assert.match(h.requests[0].path,/templates\/other\/scratch$/);assert.equal(h.$('wizard-template').value,'other');
 h.answer({scratch:{kind:'tool',from:'other'},identity:{name:'Other'}});await retry;await h.tick();
 assert.equal(h.$('wizard-template-error').hidden,true);assert.equal(h.scratch.from,'other');assert.equal(h.$('wizard-next').disabled,false);
});
await test('inherited free text is shown, can be emptied, and empty is sent as cleared',async()=>{
 const h=harness();await chosen(h);
 h.$('wizard-template').value='base';const selection=h.$('wizard-template').fire('change');await h.tick();
 h.answer({scratch:{kind:'tool'},identity:{description:'release 1.4502, commit d4e4a75f',source_repository:'https://example.invalid/src',release_notes:'https://example.invalid/v1.4502',
   license_spdx:'GPL-3.0-or-later',license_notice:'Copyright the authors'}});await selection;await h.tick();
 assert.equal(h.$('wizard-description').value,'release 1.4502, commit d4e4a75f');assert.equal(h.$('wizard-source-repository').value,'https://example.invalid/src');
 // the licence that travels with an unchanged identifier is named under the field
 h.$('wizard-license').value='GPL-3.0-or-later';await h.$('wizard-license').fire('input');
 assert.equal(h.$('wizard-license-kept').hidden,false);assert.match(h.$('wizard-license-kept').textContent,/Copyright the authors/);
 h.$('wizard-license').value='MIT';await h.$('wizard-license').fire('input');assert.equal(h.$('wizard-license-kept').hidden,true);
 h.$('wizard-description').value='';h.$('wizard-release-notes').value='';
 await h.next();await h.next();const done=h.next();const body=h.requests[0].options.body;
 assert.equal(body.description,undefined);assert.equal(body.release_notes,undefined);assert.equal(body.source_repository,'https://example.invalid/src');
 h.answer(h.valid);await done;
});
// NEW_502A: offer() protected only a NONEMPTY edit, so a field the person
// emptied read as untouched and a starting point's reply typed its text back.
// Empty where the form offered text is the person's answer; empty where the
// form itself offered nothing is still the form's, and the next reply fills it.
const BASE={scratch:{kind:'tool',from:'base'},identity:{description:'What Base is',homepage:'https://example.invalid/base',source_repository:'https://example.invalid/base-src'}};
const OTHER={scratch:{kind:'tool',from:'other'},identity:{description:'What Other is',homepage:'https://example.invalid/other',source_repository:'https://example.invalid/other-src',release_notes:'https://example.invalid/other/2.0.0'}};
async function choose(h,id){h.$('wizard-template').value=id;const selection=h.$('wizard-template').fire('change');await h.tick();return selection;}
await test('a field cleared or edited while a starting point loads is kept; untouched and form-empty fields follow it',async()=>{
 const h=harness();await chosen(h);
 let selection=await choose(h,'base');h.answer(BASE);await selection;await h.tick();
 assert.equal(h.$('wizard-description').value,'What Base is');assert.equal(h.$('wizard-release-notes').value,'');
 selection=await choose(h,'other');
 // while Other's reply is on its way: one field emptied, one rewritten
 h.$('wizard-description').value='';await h.invalidate();
 h.$('wizard-homepage').value='https://example.invalid/mine';await h.invalidate();
 h.answer(OTHER);await selection;await h.tick();
 assert.equal(h.scratch.from,'other');
 assert.equal(h.$('wizard-description').value,'','a deliberately cleared field was filled again');
 assert.equal(h.$('wizard-homepage').value,'https://example.invalid/mine');
 assert.equal(h.$('wizard-source-repository').value,'https://example.invalid/other-src','an untouched field did not follow the starting point');
 assert.equal(h.$('wizard-release-notes').value,'https://example.invalid/other/2.0.0','a field the form offered empty was not filled');
 // switching back keeps both answers, and sends the clear as cleared
 selection=await choose(h,'base');h.answer(BASE);await selection;await h.tick();
 assert.equal(h.$('wizard-description').value,'');assert.equal(h.$('wizard-homepage').value,'https://example.invalid/mine');
 assert.equal(h.$('wizard-source-repository').value,'https://example.invalid/base-src');
 assert.equal(h.$('wizard-release-notes').value,'','Base offers no release notes, so the untouched field follows it to empty');
 await h.next();await h.next();const done=h.next();const body=h.requests[0].options.body;
 assert.equal(body.description,undefined);assert.equal(body.homepage,'https://example.invalid/mine');
 h.answer(h.valid);await done;
});
await test('a field cleared while Retry asks again for a starting point is kept',async()=>{
 const h=harness();await chosen(h);
 let selection=await choose(h,'base');h.answer(BASE);await selection;await h.tick();
 selection=await choose(h,'other');h.answer({error:'that profile could not be read'},false);await selection;await h.tick();
 const retry=h.$('wizard-template-retry').fire('click');await h.tick();
 h.$('wizard-description').value='';await h.invalidate();
 h.$('wizard-source-repository').value='https://example.invalid/mine-src';await h.invalidate();
 h.answer(OTHER);await retry;await h.tick();
 assert.equal(h.scratch.from,'other');assert.equal(h.$('wizard-description').value,'');
 assert.equal(h.$('wizard-source-repository').value,'https://example.invalid/mine-src');
 assert.equal(h.$('wizard-homepage').value,'https://example.invalid/other');
 // typed back to the text the form last offered it (Base's: the cleared field
 // took nothing from Other), the field is the form's again and follows Other
 h.$('wizard-description').value='What Base is';
 selection=await choose(h,'other');h.answer(OTHER);await selection;await h.tick();
 assert.equal(h.$('wizard-description').value,'What Other is');
});
await test('a copy link for an engine shows the engine kind, not the default one',async()=>{
 // NEW_330, seen live: `#new-profile/auto-pigeon.engine.vkquake` set the
 // kind to engine and left the tab bar showing "A build pipeline".
 const h=harness();let done=false;h.AUCOM.areas['new-profile'].refresh('auto-pigeon.engine.vkquake').then(()=>done=true);
 const items={items:[{id:'auto-pigeon.engine.vkquake',kind:'engine',name:'vkQuake'}],scratch:{kind:'engine'},identity:{}};
 for(let i=0;i<50&&!done;i++){while(h.requests.length)h.answer(items);await h.tick();}
 assert.equal(done,true);assert.equal(h.$('wizard-kind').value,'engine');assert.deepEqual(h.$('wizard-kind').dispatched,['aucom:sync']);
});
console.log(`${count} profile wizard checks passed`);
