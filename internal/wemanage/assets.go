package wemanage

import (
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strings"
)

// Publications expose only the reviewed asset, never the underlying daily reports.
var assetVisible = `i.status<>'rejected' and ` + projectClueVisible + ` and not exists(select 1 from wm_project_candidate_states ps where ps.hidden and ps.name=coalesce((select canonical from wm_project_candidate_aliases where alias=trim(i.project_hint)),trim(i.project_hint))) and exists(select 1 from employees ae where ae.id=a.author_id and ae.private_work_since is null and ae.name<>'林伊默')` + ` and not exists(select 1 from wm_asset_report_sources rs join wm_custom_summaries kr on kr.id=rs.report_id where rs.asset_id=a.id and not (` + strings.ReplaceAll(projectKnowledgeVisible, "a.", "kr.") + `))`

const assetSchema = `
CREATE TABLE IF NOT EXISTS wm_assets (
 id BIGSERIAL PRIMARY KEY, source_item_id BIGINT NOT NULL REFERENCES wm_knowledge_items(id),
 kind TEXT NOT NULL CHECK(kind IN ('handbook','decision','skill')), author_id BIGINT NOT NULL REFERENCES employees(id),
 UNIQUE(source_item_id,kind)
);
CREATE TABLE IF NOT EXISTS wm_asset_versions (
 id BIGSERIAL PRIMARY KEY, asset_id BIGINT NOT NULL REFERENCES wm_assets(id), version INT NOT NULL,
 title TEXT NOT NULL, content TEXT NOT NULL, state TEXT NOT NULL CHECK(state IN ('draft','published','resolved','hidden')),
 editor_id BIGINT NOT NULL REFERENCES employees(id), created_at TIMESTAMPTZ NOT NULL DEFAULT now(), UNIQUE(asset_id,version)
);
CREATE TABLE IF NOT EXISTS wm_asset_report_sources(asset_id BIGINT NOT NULL REFERENCES wm_assets(id),report_id BIGINT NOT NULL REFERENCES wm_custom_summaries(id),PRIMARY KEY(asset_id,report_id));`

func (s *Service) assetList(w http.ResponseWriter, r *http.Request) {
	shared := r.URL.Path == "/api/shared/assets"
	filter := ""
	if shared {
		filter = " and v.state='published' and a.kind<>'decision'"
	}
	rows, err := s.Store.Pool.Query(r.Context(), `select a.id,a.source_item_id,a.kind,v.version,v.title,v.content,v.state,v.created_at::text,coalesce((select canonical from wm_project_candidate_aliases where alias=trim(i.project_hint)),trim(i.project_hint)) from wm_assets a join wm_knowledge_items i on i.id=a.source_item_id join lateral(select * from wm_asset_versions where asset_id=a.id order by version desc limit 1) v on true where `+assetVisible+` and exists(select 1 from employees ed where ed.id=v.editor_id and ed.private_work_since is null and ed.name<>'林伊默')`+filter+` order by v.created_at desc,a.id desc`)
	if err != nil {
		fail(w, 500, "读取组织资产失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, item int64
		var version int
		var kind, title, content, state, created, project string
		if err = rows.Scan(&id, &item, &kind, &version, &title, &content, &state, &created, &project); err != nil {
			fail(w, 500, "读取失败")
			return
		}
		out = append(out, map[string]any{"id": id, "source_item_id": item, "kind": kind, "version": version, "title": title, "content": content, "state": state, "created_at": created, "project": project})
	}
	if rows.Err() != nil {
		fail(w, 500, "读取失败")
		return
	}
	reply(w, 200, map[string]any{"assets": out})
}
func (s *Service) assetSave(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Source  int64  `json:"source_item_id"`
		Kind    string `json:"kind"`
		Title   string `json:"title"`
		Content string `json:"content"`
		State   string `json:"state"`
		Version int    `json:"version"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 400000)).Decode(&req) != nil || req.Source < 1 || (req.Kind != "handbook" && req.Kind != "decision" && req.Kind != "skill") || strings.TrimSpace(req.Title) == "" || strings.TrimSpace(req.Content) == "" || (req.State != "draft" && req.State != "published" && req.State != "resolved" && req.State != "hidden") || (req.Kind == "decision" && req.State == "published") {
		fail(w, 400, "检查资产类型、标题、正文和状态")
		return
	}
	tx, err := s.Store.Pool.Begin(r.Context())
	if err != nil {
		fail(w, 500, "保存失败")
		return
	}
	defer tx.Rollback(r.Context())
	var visible bool
	err = tx.QueryRow(r.Context(), `select exists(select 1 from wm_knowledge_items i where i.id=$1 and i.status<>'rejected' and `+projectClueVisible+` and not exists(select 1 from wm_project_candidate_states ps where ps.hidden and ps.name=coalesce((select canonical from wm_project_candidate_aliases where alias=trim(i.project_hint)),trim(i.project_hint))))`, req.Source).Scan(&visible)
	if err != nil || !visible {
		fail(w, 404, "来源不存在或已不可见")
		return
	}
	if who(r).employee.Name == "林伊默" {
		fail(w, 403, "私密账号不能发布共享资产")
		return
	}
	var editorVisible bool
	if err = tx.QueryRow(r.Context(), `select private_work_since is null from employees where id=$1`, who(r).employee.ID).Scan(&editorVisible); err != nil || !editorVisible {
		fail(w, 403, "私密账号不能编辑共享资产")
		return
	}
	_, err = tx.Exec(r.Context(), `insert into wm_assets(source_item_id,kind,author_id) values($1,$2,$3) on conflict do nothing`, req.Source, req.Kind, who(r).employee.ID)
	if err != nil {
		fail(w, 500, "保存失败")
		return
	}
	var id int64
	err = tx.QueryRow(r.Context(), `select a.id from wm_assets a join wm_knowledge_items i on i.id=a.source_item_id where a.source_item_id=$1 and a.kind=$2 and `+assetVisible+` for update of a`, req.Source, req.Kind).Scan(&id)
	if err != nil {
		fail(w, 404, "资产不可见")
		return
	}
	var version int
	if err = tx.QueryRow(r.Context(), `select coalesce(max(version),0) from wm_asset_versions where asset_id=$1`, id).Scan(&version); err != nil {
		fail(w, 500, "读取版本失败")
		return
	}
	if version != req.Version {
		fail(w, 409, "内容已更新，请刷新后重新编辑")
		return
	}
	_, err = tx.Exec(r.Context(), `insert into wm_asset_versions(asset_id,version,title,content,state,editor_id) values($1,$2,$3,$4,$5,$6)`, id, version+1, req.Title, req.Content, req.State, who(r).employee.ID)
	if err != nil {
		fail(w, 500, "保存版本失败")
		return
	}
	if tx.Commit(r.Context()) != nil {
		fail(w, 500, "保存失败")
		return
	}
	reply(w, 200, map[string]any{"id": id, "version": version + 1})
}
func (s *Service) assetHistory(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	if err != nil {
		fail(w, 400, "资产编号无效")
		return
	}
	rows, err := s.Store.Pool.Query(r.Context(), `select v.version,v.title,v.content,v.state,v.created_at::text from wm_asset_versions v join wm_assets a on a.id=v.asset_id join wm_knowledge_items i on i.id=a.source_item_id join employees ed on ed.id=v.editor_id where a.id=$1 and `+assetVisible+` and ed.private_work_since is null and ed.name<>'林伊默' order by v.version desc`, id)
	if err != nil {
		fail(w, 500, "读取历史失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var version int
		var title, content, state, created string
		if rows.Scan(&version, &title, &content, &state, &created) != nil {
			fail(w, 500, "读取历史失败")
			return
		}
		out = append(out, map[string]any{"version": version, "title": title, "content": content, "state": state, "created_at": created})
	}
	if rows.Err() != nil {
		fail(w, 500, "读取失败")
		return
	}
	reply(w, 200, map[string]any{"versions": out})
}

// Import existing analysis without a second model call. Never overwrite reviewed versions.
func (s *Service) assetSeed(w http.ResponseWriter, r *http.Request) {
	tx, err := s.Store.Pool.Begin(r.Context())
	if err != nil {
		fail(w, 500, "整理失败")
		return
	}
	defer tx.Rollback(r.Context())
	var visible bool
	err = tx.QueryRow(r.Context(), `select private_work_since is null and name<>'林伊默' from employees where id=$1`, who(r).employee.ID).Scan(&visible)
	if err != nil || !visible {
		fail(w, 403, "私密账号不能创建共享资产")
		return
	}
	rows, err := tx.Query(r.Context(), `select i.id,case when i.category='project' then 'handbook' when i.category='problem' then 'decision' else 'skill' end,i.title,i.content,i.project_hint,coalesce((select d.skill_md from wm_skill_drafts d where d.source_item_id=i.id and d.status<>'rejected' order by d.id desc limit 1),'') from wm_knowledge_items i join wm_knowledge_runs run on run.id=i.run_id where run.status='done' and i.status<>'rejected' and i.category in ('project','problem','skill') and `+projectClueVisible+` and not exists(select 1 from wm_project_candidate_states ps where ps.hidden and ps.name=coalesce((select canonical from wm_project_candidate_aliases where alias=trim(i.project_hint)),trim(i.project_hint))) order by i.id`)
	if err != nil {
		fail(w, 500, "读取来源失败")
		return
	}
	type source struct {
		id                                   int64
		kind, title, content, project, skill string
	}
	sources := []source{}
	for rows.Next() {
		var c source
		if rows.Scan(&c.id, &c.kind, &c.title, &c.content, &c.project, &c.skill) != nil {
			rows.Close()
			fail(w, 500, "读取来源失败")
			return
		}
		sources = append(sources, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, 500, "读取来源失败")
		return
	}
	count := 0
	for _, c := range sources {
		var id int64
		err = tx.QueryRow(r.Context(), `insert into wm_assets(source_item_id,kind,author_id) values($1,$2,$3) on conflict do nothing returning id`, c.id, c.kind, who(r).employee.ID).Scan(&id)
		if err == pgx.ErrNoRows {
			continue
		}
		if err != nil {
			fail(w, 500, "建立资产失败")
			return
		}
		heading := "工作线索"
		if c.kind == "decision" {
			heading = "待核对的问题"
		}
		if c.kind == "skill" {
			heading = "方法线索"
		}
		content := "## " + heading + "\n" + c.content
		if c.kind == "handbook" {
			content += "\n\n## 目标、阶段与职责\n待结合项目时间线核对\n\n## 文档与仓库\n待补充\n\n## 关键决定与约束\n待核对"
		}
		if c.kind == "decision" {
			content += "\n\n## 需要负责人决定什么\n待确认是否仍需决策\n\n## 选项与依据\n待核对\n\n## 最终决定\n待处理"
		}
		if c.kind == "skill" {
			content += "\n\n## 何时使用\n待核对\n\n## 输入与权限\n待补充\n\n## 操作步骤与分支\n待核对\n\n## 验证与停止条件\n待补充"
		}
		if c.kind == "skill" && c.skill != "" {
			content = c.skill
		}
		content += "\n\n## 来源\n项目：" + c.project + "\n来源线索：" + fmt.Sprint(c.id) + "\n自动整理草稿，尚未经人工确认。"
		_, err = tx.Exec(r.Context(), `insert into wm_asset_versions(asset_id,version,title,content,state,editor_id) values($1,1,$2,$3,'draft',$4)`, id, c.title, content, who(r).employee.ID)
		if err != nil {
			fail(w, 500, "保存草稿失败")
			return
		}
		count++
	}
	if tx.Commit(r.Context()) != nil {
		fail(w, 500, "整理失败")
		return
	}
	reply(w, 200, map[string]any{"created": count})
}
