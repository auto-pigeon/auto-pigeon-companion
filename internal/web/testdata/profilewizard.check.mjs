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
  const AUCOM={$,el,areas:{},t:x=>x,programFileName:x=>x,
    api:(path,options)=>new Promise(resolve=>requests.push({path,options,resolve})),
    setMessage:(id,text,kind)=>messages.push({id,text,kind}),record(){},badge:x=>el('span',{text:x}),
    withBusy:async(n,fn)=>{n.disabled=true;try{return await fn();}finally{n.disabled=false;}},
    scratch:{request:()=>scratch,stageTokens:()=>tokens,render(){},clear(){},fill(value){Object.assign(scratch,value);},refresh:async()=>{}},
    showArea(){},status:{},openInstalledProfile:async id=>installs.push(id)};
  const document={createTextNode:x=>x,querySelectorAll:()=>[],body:{classList:{remove(){}}}};
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
await test('a delayed template cannot mix a changed draft',async()=>{
 const h=harness();const init=h.AUCOM.areas['new-profile'].refresh('');await h.tick();h.answer({items:[{id:'base',kind:'tool',name:'Base'}]});await init;
 h.$('wizard-template').value='base';const selection=h.$('wizard-template').fire('change');await h.tick();h.$('wizard-name').value='Edited';await h.invalidate();h.answer({scratch:{kind:'engine'},identity:{name:'Wrong'}});await selection;await h.tick();
 assert.equal(h.$('wizard-name').value,'Edited');assert.equal(h.scratch.kind,'tool');
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
