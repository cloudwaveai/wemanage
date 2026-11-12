import {test} from 'node:test';
import assert from 'node:assert/strict';
import {total,compare,riskMarkup} from '../static/usage-analysis.js';
import {sidebar} from '../static/navigation.js';
test('token arithmetic does not add cache or reasoning twice',()=>{assert.equal(total({input:100,cached:90,output:20,reasoning:10}),120);assert.equal(compare(150,100),'+50.0%');assert.equal(compare(20,0),'无对照用量')});
test('risk content is escaped and warn is distinct from blocked and resolved',()=>{const event={employee:'<img src=x onerror=alert(1)>',snippet:'避免杀掉 <script>x</script>',rule:'av-evasion',kind:'content_flagged',employee_id:1,at:'2026-11-12T09:34:00Z',matched:'免杀'};let html=riskMarkup(event);assert.ok(!html.includes('<script>'));assert.ok(html.includes('&lt;script&gt;'));assert.ok(html.includes('仅提醒 · 已放行'));assert.ok(html.includes('from=2026-11-12'));assert.ok(riskMarkup({...event,blocked:true}).includes('已拦截'));assert.ok(riskMarkup({...event,resolved_at:event.at}).includes('已处理'))});
test('diagnostics is reachable in the existing admin navigation, absent for members',()=>{for(const hash of ['#token-analysis','#risk-analysis?employee_id=11']){globalThis.location={hash};let html=sidebar({admin:true});assert.equal((html.match(/nav-primary current/g)||[]).length,1);assert.ok(html.includes('Token 分析'));assert.ok(!sidebar({admin:false}).includes('href="#risk-analysis"'))}});
