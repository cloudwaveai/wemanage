const form = document.querySelector('#form');
const month = document.querySelector('#month');
const result = document.querySelector('#result');
const esc = value => String(value).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const fmt = value => Number(value).toLocaleString('zh-CN');

async function load() {
  result.textContent = '正在读取…';
  try {
    const response = await fetch('./api/reporting/usage?month=' + encodeURIComponent(month.value), {credentials:'same-origin'});
    const data = await response.json();
    if (!response.ok) throw new Error(data.error || '读取失败');
    const total = data.rows.reduce((a, r) => ({requests:a.requests+r.requests, input:a.input+r.input_tokens, output:a.output+r.output_tokens}), {requests:0,input:0,output:0});
    result.innerHTML = `<p>${fmt(total.requests)} 次调用 · 输入 ${fmt(total.input)} Token · 输出 ${fmt(total.output)} Token</p>` + (data.rows.length ? `<div style="overflow-x:auto"><table class="usage-table"><thead><tr><th>时间</th><th>人员</th><th>项目</th><th>模型</th><th>次数</th><th>输入 Token</th><th>输出 Token</th><th>估算费用</th></tr></thead><tbody>${data.rows.map(r => `<tr><td>${esc(r.hour)}</td><td>${esc(r.employee)}</td><td>${esc(r.project)}${r.category === 'client' ? ' · 客户项目' : r.category === 'infrastructure' ? ' · 基础建设' : ''}</td><td>${esc(r.model)}</td><td>${fmt(r.requests)}</td><td>${fmt(r.input_tokens)}</td><td>${fmt(r.output_tokens)}</td><td>${r.estimated_cost==null?'未配置单价':`${esc(r.currency)} ${Number(r.estimated_cost).toFixed(4)}（${fmt(r.priced_requests)}/${fmt(r.requests)} 次有单价）`}</td></tr>`).join('')}</tbody></table></div>` : '<p>这个月没有符合权限的记录。</p>');
  } catch (err) { result.textContent = err.message; }
}
month.value = new Intl.DateTimeFormat('en-CA', {timeZone:'Asia/Shanghai', year:'numeric', month:'2-digit'}).format(new Date());
form.addEventListener('submit', event => { event.preventDefault(); load(); });
load();
