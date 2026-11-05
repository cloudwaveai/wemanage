import {strict as assert} from 'node:assert';
import {test} from 'node:test';
import {sidebar} from '../static/navigation.js';
import {mountProjects} from '../static/projects.js';
import {mountManagement} from '../static/management.js';

test('all routes share six primary destinations and a single current group',()=>{
 for(const hash of ['#organization','#overview','#people/3','#projects','#project-work/Test','#project-settings/Test','#assets','#knowledge','#review-tasks','#expenses','#usage','#custom-summary','#analysis']){
  globalThis.location={hash};const html=sidebar({admin:true});
  assert.equal((html.match(/class="nav-primary /g)||[]).length,6,hash);
  assert.equal((html.match(/class="nav-primary current"/g)||[]).length,1,hash);
 }
 globalThis.location={hash:'#review-tasks'};const member=sidebar({admin:false});assert.ok(!member.includes('href="#expenses"'));assert.ok(!member.includes('href="/logs"'));
});
function root(){const elements=new Map();return {innerHTML:'',querySelectorAll:()=>[],querySelector(sel){if(!elements.has(sel))elements.set(sel,{addEventListener(){},classList:{add(){}},focus(){}});return elements.get(sel)}}}
const fixture={name:'测试项目',owner_name:'负责人',owner_id:1,id:8,status:'active',start_day:'2026-11-01',acceptance_day:'2026-12-01',payments:[],version:1};
function mockFetch(){globalThis.fetch=async url=>({ok:true,json:async()=>url.includes('project-candidates')?{projects:[{name:'零散名称',aliases:[],hidden:false,clues:[]}]}:url.includes('organization')?{members:[{id:1,name:'负责人'}]}:{projects:[fixture]}})}
test('project workbench separates registered projects from extraction names',async()=>{
 globalThis.location={hash:'#projects'};mockFetch();const r=root();await mountProjects(r,{admin:true,name:'管理员'});
 assert.ok(r.innerHTML.includes('href="#project-settings/'+encodeURIComponent(fixture.name)+'"'));
 assert.ok(r.innerHTML.includes('2026-11-01'));assert.ok(r.innerHTML.includes('2026-12-01'));
 assert.ok(!r.innerHTML.includes('class="project-name" href="#project-work/'+encodeURIComponent('零散名称')+'"'));
 assert.ok(r.innerHTML.includes('待归类'));
});
test('project maintenance deep link opens the exact project with existing values',async()=>{
 globalThis.location={hash:'#project-settings/'+encodeURIComponent(fixture.name)};mockFetch();const r=root();await mountManagement(r,{admin:true,name:'管理员'});
 assert.ok(r.innerHTML.includes('id="project-form"'));
 assert.ok(r.innerHTML.includes('value="2026-11-01"'));assert.ok(r.innerHTML.includes('value="2026-12-01"'));
 assert.ok(r.innerHTML.includes('value="测试项目"'));assert.ok(r.innerHTML.includes('readonly'));
});
test('project classification uses the existing PUT endpoint and preserves the source name',async()=>{
 globalThis.location={hash:'#projects'};const requests=[];globalThis.fetch=async(url,options)=>{requests.push([url,options]);return {ok:true,json:async()=>url.includes('project-candidates')?{projects:[{name:'待归类名称',aliases:[],hidden:false,clues:[]}]}:{projects:[]}}};
 const r=root(),button={dataset:{hide:'待归类名称'}};r.querySelectorAll=sel=>sel==='[data-hide],[data-restore]'?[button]:[];
 await mountProjects(r,{admin:true,name:'管理员',csrf_token:'test'});await button.onclick();
 const write=requests.find(([,o])=>o.method);assert.equal(write[1].method,'PUT');assert.deepEqual(JSON.parse(write[1].body),{action:'hide',name:'待归类名称'});assert.equal(write[1].headers['X-CSRF-Token'],'test');
});
