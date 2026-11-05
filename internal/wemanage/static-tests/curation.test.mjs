import {strict as assert} from 'node:assert';
import {test} from 'node:test';
import {skillFlowHTML,skillContentHTML} from '../static/skill-flow.js';
import {sidebar} from '../static/navigation.js';
const flow=[{id:'S1',text:'检查 <script> 数据',next:[{condition:'成功',target:'S2'},{condition:'重试',target:'S1'}]},{id:'S2',text:'人工核对',next:[]}];
test('skill preview preserves branches and escapes source text',()=>{
 const html=skillFlowHTML(flow);assert.ok(html.includes('S1 → S2'));assert.ok(html.includes('S1 → S1'));assert.ok(html.includes('成功'));assert.ok(html.includes('重试'));assert.ok(!html.includes('<script>'));assert.ok(html.includes('&lt;script&gt;'));
 const content='## 何时使用\n完整正文\n```skill-flow\n'+JSON.stringify(flow)+'\n```';const preview=skillContentHTML(content);assert.ok(preview.includes('<svg'));assert.ok(preview.includes('完整正文'));assert.ok(preview.includes('完整 Skill 原文'));
 assert.ok(skillFlowHTML([{id:'S1',text:'错误',next:[{target:'unknown'}]}]).includes('流程引用无效'));
});
test('knowledge review shares existing navigation and is admin-only in the menu',()=>{
 globalThis.location={hash:'#curation'};const admin=sidebar({admin:true});assert.ok(admin.includes('href="#curation"'));assert.equal((admin.match(/nav-primary current/g)||[]).length,1);assert.ok(!sidebar({admin:false}).includes('href="#curation"'));
});
