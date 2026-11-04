package wemanage

import (
	"fmt"
	"net/http"
	"strings"
)

// Every derived asset retains all report sources, including later privacy changes.
const projectKnowledgeVisible = `a.analysis_kind='knowledge' and a.state='done' and exists(select 1 from employees owner where owner.id=a.owner_id and owner.private_work_since is null and owner.name<>'林伊默') and not exists(select 1 from wm_project_candidate_states ps where ps.name=a.project_name and ps.hidden) and not exists(select 1 from projects p join wm_hidden_projects h on h.project_id=p.id where p.name=a.project_name) and not exists(select 1 from wm_project_report_items pi join wm_knowledge_items i on i.id=pi.item_id where pi.report_id=a.id and (i.status='rejected' or not (` + projectClueVisible + `))) and not exists(select 1 from wm_custom_summary_sources x join employees e on e.id=x.employee_id where x.report_id=a.id and (e.private_work_since is not null or e.name='林伊默' or exists(select 1 from requests r left join api_keys k on k.id=r.api_key_id left join request_projects rp on rp.request_id=r.id join wm_hidden_projects hp on hp.project_id=coalesce(rp.project_id,k.project_id) where r.employee_id=e.id and (r.started_at at time zone 'Asia/Shanghai')::date=x.source_day)))`

func knowledgeMarkdown(k projectKnowledge) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## 项目概述\n%s\n", k.Overview)
	for index, entries := range [][]knowledgeEntry{k.Knowledge, k.Issues, k.Skills} {
		fmt.Fprintf(&b, "\n## %s\n", []string{"项目知识", "问题与处理状态", "可复用方法"}[index])
		for _, e := range entries {
			fmt.Fprintf(&b, "\n### %s\n状态：%s\n%s\n依据：%s\n", e.Title, e.Status, e.Body, strings.Join(e.FactIDs, "、"))
			for _, node := range e.Flow {
				fmt.Fprintf(&b, "\n%s：%s\n", node.ID, node.Text)
				for _, edge := range node.Next {
					fmt.Fprintf(&b, "- %s → %s\n", edge.Condition, edge.Target)
				}
				if len(node.Next) == 1 {
					b.WriteString("其他结果：当前流程未覆盖，停止并人工核查。\n")
				}
				if len(node.Next) == 0 {
					b.WriteString("结束 / 人工确认\n")
				}
			}
		}
	}
	b.WriteString("\n## 证据缺口\n")
	for _, gap := range k.Gaps {
		fmt.Fprintf(&b, "- %s\n", gap)
	}
	b.WriteString("\n## 证据账本\n")
	for _, fact := range k.Facts {
		fmt.Fprintf(&b, "\n%s · %s · %s\n%s\n", fact.ID, fact.Date, fact.Status, fact.Statement)
		for _, q := range fact.Evidence {
			fmt.Fprintf(&b, "来源 %d：%s\n", q.Source, q.Quote)
		}
	}
	return b.String()
}
func (s *Service) archiveProjectKnowledge(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, 400, "知识版本无效")
		return
	}
	tx, err := s.Store.Pool.Begin(r.Context())
	if err != nil {
		fail(w, 500, "保存失败")
		return
	}
	defer tx.Rollback(r.Context())
	var result, project string
	var source int64
	err = tx.QueryRow(r.Context(), `select a.result,a.project_name,(select min(item_id) from wm_project_report_items where report_id=a.id) from wm_custom_summaries a where a.id=$1 and a.owner_id=$2 and `+projectKnowledgeVisible+` for share of a`, id, who(r).employee.ID).Scan(&result, &project, &source)
	if err != nil {
		fail(w, 404, "知识版本不可见或未完成")
		return
	}
	var k projectKnowledge
	if decodeKnowledge(result, &k) != nil {
		fail(w, 422, "知识内容无效")
		return
	}
	_, err = tx.Exec(r.Context(), `insert into wm_assets(source_item_id,kind,author_id) values($1,'handbook',$2) on conflict do nothing`, source, who(r).employee.ID)
	if err != nil {
		fail(w, 500, "创建资产失败")
		return
	}
	var asset int64
	err = tx.QueryRow(r.Context(), `select a.id from wm_assets a join wm_knowledge_items i on i.id=a.source_item_id where source_item_id=$1 and kind='handbook' and `+assetVisible+` for update of a`, source).Scan(&asset)
	if err != nil {
		fail(w, 404, "资产不可见")
		return
	}
	var existing bool
	if err = tx.QueryRow(r.Context(), `select exists(select 1 from wm_asset_report_sources where asset_id=$1 and report_id=$2)`, asset, id).Scan(&existing); err != nil {
		fail(w, 500, "检查版本失败")
		return
	}
	if existing {
		reply(w, 200, map[string]any{"id": asset, "reused": true})
		return
	}
	_, err = tx.Exec(r.Context(), `insert into wm_asset_versions(asset_id,version,title,content,state,editor_id) select $1,coalesce(max(version),0)+1,$2,$3,'draft',$4 from wm_asset_versions where asset_id=$1`, asset, project+" · 项目知识", knowledgeMarkdown(k), who(r).employee.ID)
	if err != nil {
		fail(w, 500, "保存草稿失败")
		return
	}
	_, err = tx.Exec(r.Context(), `insert into wm_asset_report_sources(asset_id,report_id) values($1,$2)`, asset, id)
	if err != nil {
		fail(w, 500, "保存全部来源关联失败")
		return
	}
	if tx.Commit(r.Context()) != nil {
		fail(w, 500, "保存失败")
		return
	}
	reply(w, 200, map[string]any{"id": asset, "state": "draft"})
}
