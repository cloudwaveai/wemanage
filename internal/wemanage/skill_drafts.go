package wemanage

import (
	"encoding/json"
	"net/http"
)

// A draft and its visual representation share the privacy of their sources.
const skillVisible = `not exists(select 1 from wm_knowledge_sources src join employees emp on emp.id=src.employee_id where src.run_id=i.run_id and (emp.private_work_since is not null or emp.name='林伊默'))`

func (s *Service) listSkillDrafts(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Pool.Query(r.Context(), `select d.id,d.source_item_id,coalesce(d.source_focus_id,0),d.title,d.trigger_text,d.steps,d.verification,d.limits_text,d.skill_md,d.status,i.title,i.evidence_verified,run.kind,run.period_start::text,run.period_end::text,coalesce(f.result,'') from wm_skill_drafts d join wm_knowledge_items i on i.id=d.source_item_id join wm_knowledge_runs run on run.id=i.run_id left join wm_focus_jobs f on f.id=d.source_focus_id where `+skillVisible+` order by d.id`)
	if err != nil {
		fail(w, 500, "读取 Skill 草稿失败")
		return
	}
	defer rows.Close()
	type draft struct {
		ID               int64           `json:"id"`
		ItemID           int64           `json:"item_id"`
		FocusID          int64           `json:"focus_id"`
		Title            string          `json:"title"`
		Trigger          string          `json:"trigger"`
		Steps            json.RawMessage `json:"steps"`
		Verification     string          `json:"verification"`
		Limits           string          `json:"limits"`
		Markdown         string          `json:"markdown"`
		Status           string          `json:"status"`
		SourceTitle      string          `json:"source_title"`
		EvidenceVerified bool            `json:"evidence_verified"`
		Kind             string          `json:"kind"`
		From             string          `json:"from"`
		To               string          `json:"to_exclusive"`
		FocusResult      string          `json:"focus_result"`
	}
	out := []draft{}
	for rows.Next() {
		var d draft
		if rows.Scan(&d.ID, &d.ItemID, &d.FocusID, &d.Title, &d.Trigger, &d.Steps, &d.Verification, &d.Limits, &d.Markdown, &d.Status, &d.SourceTitle, &d.EvidenceVerified, &d.Kind, &d.From, &d.To, &d.FocusResult) != nil {
			fail(w, 500, "读取 Skill 草稿失败")
			return
		}
		out = append(out, d)
	}
	if rows.Err() != nil {
		fail(w, 500, "读取 Skill 草稿失败")
		return
	}
	reply(w, 200, map[string]any{"drafts": out})
}
func (s *Service) reviewSkillDraft(w http.ResponseWriter, r *http.Request) {
	id, err := idParam(r)
	var req struct {
		Status string `json:"status"`
	}
	if err != nil || json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req) != nil || (req.Status != "confirmed" && req.Status != "rejected") {
		fail(w, 400, "审核状态无效")
		return
	}
	tag, err := s.Store.Pool.Exec(r.Context(), `update wm_skill_drafts d set status=$2,reviewed_by=$3,reviewed_at=now() from wm_knowledge_items i where d.id=$1 and i.id=d.source_item_id and `+skillVisible, id, req.Status, who(r).employee.ID)
	if err != nil {
		fail(w, 500, "保存审核失败")
		return
	}
	if tag.RowsAffected() != 1 {
		fail(w, 404, "草稿不存在或无权查看")
		return
	}
	reply(w, 200, map[string]any{"status": req.Status})
}
