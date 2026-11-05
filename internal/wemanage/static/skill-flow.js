const esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
let serial=0;
// Text is wrapped, never clipped or replaced by model-generated HTML.
function lines(text,width){return String(text).split('\n').flatMap(line=>{const out=[];let row='',size=0;for(const ch of line){const n=ch.charCodeAt(0)>255?1:.55;if(size+n>width){out.push(row);row='';size=0}row+=ch;size+=n}out.push(row);return out})}
export function skillFlowHTML(nodes){
 if(!Array.isArray(nodes)||!nodes.length)return '<p>尚无可核对的结构化流程。</p>';
 const ids=new Set(nodes.map(n=>n.id));if(ids.size!==nodes.length||nodes.some(n=>!n.id||typeof n.text!=='string'||!Array.isArray(n.next)||n.next.some(e=>!ids.has(e.target))))return '<p role="alert">流程引用无效，请核对原文。</p>';
 const marker='flow-arrow-'+(++serial);let y=30;const positions=new Map();
 const rows=nodes.map(n=>{const text=lines(n.text,20),edges=n.next.map(e=>({...e,lines:lines(e.condition,24)}));const height=48+text.length*22;const gap=Math.max(55,edges.reduce((sum,e)=>sum+e.lines.length*19+35,0));const row={...n,textLines:text,edges,y,height};positions.set(n.id,row);y+=height+gap;return row});
 const width=920;
 const paths=rows.flatMap((n,i)=>n.edges.map((e,j)=>{const target=positions.get(e.target),start=n.y+n.height/2,finish=target.y+target.height/2;const lane=410+((i+j)%6)*18;const path=n.id===e.target?`M 370 ${start} H ${lane} V ${start+25} H 385 V ${start+12} H 370`:`M 370 ${start} H ${lane} V ${finish} H 370`;return `<path class="sf-edge" d="${path}" marker-end="url(#${marker})"/>`})).join('');
 const cards=rows.map(n=>`<g><rect class="sf-node ${n.next.length?'':'sf-terminal'}" x="30" y="${n.y}" width="340" height="${n.height}" rx="8"/><text class="sf-id" x="48" y="${n.y+24}">${esc(n.id)}${!n.next.length?' · 结束 / 人工确认':''}</text><text class="sf-text">${n.textLines.map((t,i)=>`<tspan x="48" y="${n.y+49+i*22}">${esc(t)}</tspan>`).join('')}</text></g>`).join('');
 const labels=rows.map(n=>{let offset=0;return n.edges.map(e=>{const label=`<g><text class="sf-branch" x="550" y="${n.y+24+offset}">${esc(n.id)} → ${esc(e.target)}</text><text class="sf-text">${e.lines.map((t,i)=>`<tspan x="550" y="${n.y+46+offset+i*19}">${esc(t)}</tspan>`).join('')}</text></g>`;offset+=e.lines.length*19+35;return label}).join('')}).join('');
 return `<div class="skill-flow-scroll" tabindex="0" aria-label="Skill 条件流程图，可横向滚动"><svg class="skill-flow-svg" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${width} ${y}" role="img" aria-label="操作节点、条件和流向"><defs><marker id="${marker}" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" fill="#64748b"/></marker></defs>${paths}${cards}${labels}</svg></div>`;
}
export function skillContentHTML(content){const match=String(content).match(/```skill-flow\s*\n([\s\S]*?)\n```/);let flow='';if(match){try{flow=skillFlowHTML(JSON.parse(match[1]))}catch{flow='<p role="alert">流程格式无法读取，原文已保留。</p>'}}return flow+`<details ${flow?'':'open'}><summary>完整 Skill 原文</summary><pre class="pw-content">${esc(content)}</pre></details>`}
