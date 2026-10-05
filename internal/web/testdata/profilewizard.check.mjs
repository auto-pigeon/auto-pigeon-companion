import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';
import assert from 'node:assert/strict';

// Drive the shipped event handlers with controlled network completion order.
// Live browser acceptance separately exercises the real controls/server.
function harness() {
  const nodes=new Map(), requests=[], messages=[], installs=[];
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
    after(){} focus(){this.focused=true;} remove(){} scrollIntoView(){}
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
  runInNewContext(readFileSync(new URL('../assets/profiles.js',import.meta.url),'utf8'),{window:{AUCOM},document,console,structuredClone,URLSearchParams,Event:class{},navigator:{}});
  // The shipped file installs this navigation helper; no navigation is needed here.
  AUCOM.openInstalledProfile=async id=>installs.push(id);
  const valid={valid:true,id:'local.a',kind:'tool',name:'A tool',version:'1.0.0',trust:'local',document:{name:'A tool',publisher:{name:'Local'},license:{spdx:'NOASSERTION'},executables:[],actions:[]}};
  return {$,AUCOM,requests,messages,installs,scratch,valid,setTokens:value=>tokens=value,
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
await test('failed stage argument save stays incomplete and retries without import',async()=>{
 const h=harness();h.setTokens([{stage:'compile',arguments:['-verbose']}]);await h.next();await h.next();const done=h.next();h.answer(h.valid);await done;
 const install=h.$('wizard-import').fire('click');h.answer({id:'local.a',name:'A tool'});await h.tick();assert.match(h.requests[0].path,/stage-arguments/);h.answer({error:'disk full'},false);await install;
 assert.deepEqual(h.installs,[]);assert.match(h.messages.at(-1).text,/setup is incomplete/);assert.equal(h.$('wizard-import').textContent,'Retry saving stage arguments');
 const retry=h.$('wizard-import').fire('click');assert.match(h.requests[0].path,/stage-arguments/);h.answer({});await retry;assert.deepEqual(h.installs,['local.a']);
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
console.log(`${count} profile wizard checks passed`);
