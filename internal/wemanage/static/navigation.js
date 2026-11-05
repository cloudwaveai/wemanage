const icons={organization:'M4 4h6v6H4z M14 4h6v6h-6z M4 14h6v6H4z M14 14h6v6h-6z',people:'M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2 M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8 M20 21v-2a4 4 0 0 0-3-3.87 M16 3.13a4 4 0 0 1 0 7.75',projects:'M3 7h7l2-3h9v16H3z',knowledge:'M4 3h7v17H4z M14 3h6v17h-6z',expenses:'M3 5h18v15H3z M3 9h18 M15 15h3',summary:'M5 3h14v18H5z M8 8h8 M8 12h8 M8 16h5'};
export function sidebar(session={admin:true}) {
 const hash=location.hash||'#organization',route=hash.split('/')[0];
 const groups=session.admin?[
 ['organization','组织','#organization',['#organization','#overview'],[['#organization','组织与成员'],['#overview','工作概览']]],
 ['people','人员','#people',['#people'],[]],
 ['projects','项目','#projects',['#projects','#managed-projects','#project-work','#project-settings'],[['#projects','项目工作台'],['#managed-projects','负责人维护']]],
 ['knowledge','知识库','#assets',['#assets','#knowledge','#review-tasks'],[['#assets','可复用资产'],['#knowledge','提取与整理'],['#review-tasks','专家审核']]],
 ['expenses','费用与用量','#expenses',['#expenses','#usage'],[['#expenses','订阅与实付'],['#usage','模型用量']]],
 ['summary','总结','#custom-summary',['#custom-summary','#analysis'],[['#custom-summary','自定义总结'],['#analysis','日志分析']]]
 ]:[['projects','我的项目','#managed-projects',['#managed-projects','#project-settings'],[]],['knowledge','知识库','#assets',['#assets','#review-tasks'],[['#assets','可复用资产'],['#review-tasks','我的审核']]]];
 return `<aside class="sidebar"><a class="brand" href="${session.admin?'#organization':'#assets'}"><span class="brand-icon">W</span><strong>We-Manage</strong></a><nav class="nav" aria-label="主导航">${groups.map(([key,label,url,routes,children])=>{const current=routes.includes(route);return `<div class="nav-group"><a href="${url}" class="nav-primary ${current?'current':''}" ${current?'aria-current="true"':''}><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><path d="${icons[key]}"/></svg>${label}</a>${current&&children.length?`<div class="nav-children">${children.map(([u,l])=>`<a href="${u}" ${hash===u?'class="selected" aria-current="page"':''}>${l}</a>`).join('')}</div>`:''}</div>`}).join('')}</nav><div class="nav-footer"><a href="/">打开网关 ↗</a>${session.admin?'<details><summary>数据入口</summary><a href="./usage.html">用量明细 ↗</a><a href="/logs">原始记录 ↗</a></details>':''}</div></aside>`;
}
