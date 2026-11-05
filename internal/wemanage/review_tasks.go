package wemanage

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *Service) reviewTasks(w http.ResponseWriter, r *http.Request) {
	rows, e := s.Store.Pool.Query(r.Context(), `select t.id,t.asset_id,t.version,t.reviewer_id,e.name,t.state,t.note,v.title,v.content,a.kind,t.version=(select max(version) from wm_asset_versions where asset_id=a.id) from wm_asset_review_tasks t join wm_assets a on a.id=t.asset_id join wm_knowledge_items i on i.id=a.source_item_id join wm_asset_versions v on v.asset_id=a.id and v.version=t.version join employees e on e.id=t.reviewer_id join employees ed on ed.id=v.editor_id where ($1 or t.reviewer_id=$2) and `+humanVisible+` and ed.private_work_since is null and ed.name<>'林伊默' and `+assetVisible+` order by t.updated_at desc,t.id desc`, who(r).employee.IsSuperAdmin, who(r).employee.ID)
	if e != nil {
		fail(w, 500, "读取审核任务失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, asset, reviewer int64
		var ver int
		var name, state, note, title, content, kind string
		var current bool
		if rows.Scan(&id, &asset, &ver, &reviewer, &name, &state, &note, &title, &content, &kind, &current) != nil {
			fail(w, 500, "读取失败")
			return
		}
		out = append(out, map[string]any{"id": id, "asset_id": asset, "version": ver, "reviewer_id": reviewer, "reviewer": name, "state": state, "note": note, "title": title, "content": content, "kind": kind, "current": current})
	}
	if rows.Err() != nil {
		fail(w, 500, "读取失败")
		return
	}
	reply(w, 200, map[string]any{"tasks": out})
}
func (s *Service) saveReviewTask(w http.ResponseWriter, r *http.Request) {
	var q struct {
		ID       int64  `json:"id"`
		Asset    int64  `json:"asset_id"`
		Version  int    `json:"version"`
		Reviewer int64  `json:"reviewer_id"`
		Action   string `json:"action"`
		Note     string `json:"note"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 30000)).Decode(&q) != nil {
		fail(w, 400, "审核格式无效")
		return
	}
	u := who(r).employee
	tx, e := s.Store.Pool.Begin(r.Context())
	if e != nil {
		fail(w, 500, "保存失败")
		return
	}
	defer tx.Rollback(r.Context())
	var editorOK bool
	e = tx.QueryRow(r.Context(), `select exists(select 1 from employees e where e.id=$1 and `+humanVisible+`)`, u.ID).Scan(&editorOK)
	if e != nil || !editorOK {
		fail(w, 403, "审核仅限可见飞书成员")
		return
	}
	if q.Action == "assign" {
		if !u.IsSuperAdmin {
			fail(w, 403, "仅管理员可以分配审核")
			return
		}
		var ok bool
		e = tx.QueryRow(r.Context(), `select exists(select 1 from employees e where e.id=$1 and `+humanVisible+`)`, q.Reviewer).Scan(&ok)
		if e != nil || !ok {
			fail(w, 400, "审核人必须是飞书成员")
			return
		}
		var id int64
		e = tx.QueryRow(r.Context(), `select a.id from wm_assets a join wm_knowledge_items i on i.id=a.source_item_id where a.id=$1 and `+assetVisible+` for update of a`, q.Asset).Scan(&id)
		if e != nil {
			fail(w, 404, "资产不可见")
			return
		}
		var latest int
		var state string
		e = tx.QueryRow(r.Context(), `select v.version,v.state from wm_asset_versions v join employees ed on ed.id=v.editor_id where v.asset_id=$1 and ed.private_work_since is null and ed.name<>'林伊默' and v.version=(select max(version) from wm_asset_versions where asset_id=$1)`, q.Asset).Scan(&latest, &state)
		if e != nil || latest != q.Version || state != "draft" {
			fail(w, 409, "只能分配最新草稿，请刷新")
			return
		}
		e = tx.QueryRow(r.Context(), `insert into wm_asset_review_tasks(asset_id,version,reviewer_id,assigned_by,note) values($1,$2,$3,$4,$5) on conflict(asset_id,version,reviewer_id) do update set assigned_by=wm_asset_review_tasks.assigned_by returning id`, q.Asset, q.Version, q.Reviewer, u.ID, q.Note).Scan(&q.ID)
	} else {
		if q.Action != "approved" && q.Action != "changes" && q.Action != "deferred" {
			fail(w, 400, "审核动作无效")
			return
		}
		if strings.TrimSpace(q.Note) == "" {
			fail(w, 400, "请填写判断依据或待补充条件")
			return
		}
		var reviewer int64
		var state string
		e = tx.QueryRow(r.Context(), `select t.asset_id,t.version,t.reviewer_id,t.state from wm_asset_review_tasks t join wm_assets a on a.id=t.asset_id join wm_knowledge_items i on i.id=a.source_item_id where t.id=$1 and `+assetVisible+` for update of t,a`, q.ID).Scan(&q.Asset, &q.Version, &reviewer, &state)
		if e != nil {
			fail(w, 404, "任务不可见")
			return
		}
		if reviewer != u.ID {
			fail(w, 403, "由指定审核人作出判断")
			return
		}
		if state == "approved" || state == "changes" {
			fail(w, 409, "该次审核已结束")
			return
		}
		var ver int
		var assetState string
		e = tx.QueryRow(r.Context(), `select v.version,v.state from wm_asset_versions v join employees ed on ed.id=v.editor_id where v.asset_id=$1 and ed.private_work_since is null and ed.name<>'林伊默' and v.version=(select max(version) from wm_asset_versions where asset_id=$1)`, q.Asset).Scan(&ver, &assetState)
		if e != nil || ver != q.Version || assetState != "draft" {
			fail(w, 409, "草稿已变化，请管理员重新分配新版本")
			return
		}
		if q.Action == "approved" {
			_, e = tx.Exec(r.Context(), `insert into wm_asset_versions(asset_id,version,title,content,state,editor_id) select v.asset_id,v.version+1,v.title,v.content,case when a.kind='decision' then 'resolved' else 'published' end,$3 from wm_asset_versions v join wm_assets a on a.id=v.asset_id where v.asset_id=$1 and v.version=$2`, q.Asset, q.Version, u.ID)
			if e != nil {
				fail(w, 500, "发布审核版本失败")
				return
			}
		}
		_, e = tx.Exec(r.Context(), `update wm_asset_review_tasks set state=$2,note=$3,updated_at=now() where id=$1`, q.ID, q.Action, q.Note)
	}
	if e != nil {
		fail(w, 500, "保存审核失败")
		return
	}
	_, e = tx.Exec(r.Context(), `insert into wm_review_task_history(task_id,action,note,actor_id)values($1,$2,$3,$4)`, q.ID, q.Action, q.Note, u.ID)
	if e != nil || tx.Commit(r.Context()) != nil {
		fail(w, 500, "保存审核失败")
		return
	}
	reply(w, 200, map[string]any{"id": q.ID})
}

func (s *Service) reviewTaskHistory(w http.ResponseWriter, r *http.Request) {
	id, e := idParam(r)
	if e != nil {
		fail(w, 400, "审核编号无效")
		return
	}
	rows, e := s.Store.Pool.Query(r.Context(), `select h.action,h.note,h.created_at::text,e.name from wm_review_task_history h join wm_asset_review_tasks t on t.id=h.task_id join wm_assets a on a.id=t.asset_id join wm_knowledge_items i on i.id=a.source_item_id join employees e on e.id=h.actor_id where t.id=$1 and ($2 or t.reviewer_id=$3) and `+humanVisible+` and `+assetVisible+` order by h.id desc`, id, who(r).employee.IsSuperAdmin, who(r).employee.ID)
	if e != nil {
		fail(w, 500, "读取审核记录失败")
		return
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var action, note, at, name string
		if rows.Scan(&action, &note, &at, &name) != nil {
			fail(w, 500, "读取失败")
			return
		}
		out = append(out, map[string]string{"action": action, "note": note, "created_at": at, "actor": name})
	}
	if rows.Err() != nil {
		fail(w, 500, "读取失败")
		return
	}
	reply(w, 200, map[string]any{"history": out})
}
