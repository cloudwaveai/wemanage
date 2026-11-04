package wemanage

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The candidate's cited digest sources are the only allowed search scope.
const focusVisible = `not exists(select 1 from wm_knowledge_sources x join employees e on e.id=x.employee_id where x.run_id=i.run_id and (e.private_work_since is not null or e.name='林伊默'))`
const focusScope = `exists(select 1 from wm_knowledge_sources src join wm_knowledge_items i on i.run_id=src.run_id cross join lateral jsonb_array_elements(i.evidence) ev where i.id=$1 and ` + focusVisible + ` and src.ordinal=(ev->>'source')::int and src.employee_id=r.employee_id and r.started_at >= (src.source_day::timestamp at time zone 'Asia/Shanghai') and r.started_at < ((src.source_day+1)::timestamp at time zone 'Asia/Shanghai'))`

func (s *Service) searchFocus(w http.ResponseWriter, r *http.Request) {
	id, e := idParam(r)
	term := strings.TrimSpace(r.URL.Query().Get("q"))
	if e != nil || len([]rune(term)) < 2 || len([]rune(term)) > 80 {
		fail(w, 400, "请输入 2 至 80 个字的关键词")
		return
	}
	var visible bool
	if e = s.Store.Pool.QueryRow(r.Context(), `select exists(select 1 from wm_knowledge_items i where i.id=$1 and `+focusVisible+`)`, id).Scan(&visible); e != nil || !visible {
		fail(w, 404, "线索不存在或无权查看")
		return
	}
	// The searchable index is a locator only; the worker reconstructs raw records.
	rows, e := s.Store.Pool.Query(r.Context(), `select r.id::text,r.started_at,e.name from requests r join employees e on e.id=r.employee_id where `+focusScope+` and exists(select 1 from request_texts t where t.request_id=r.id and position(lower($2) in lower(t.text))>0) order by r.started_at desc limit 21`, id, term)
	if e != nil {
		fail(w, 500, "搜索日志失败")
		return
	}
	defer rows.Close()
	type hit struct {
		ID   string `json:"id"`
		Time string `json:"time"`
		Name string `json:"name"`
	}
	out := []hit{}
	for rows.Next() {
		var h hit
		var at time.Time
		if rows.Scan(&h.ID, &at, &h.Name) != nil {
			fail(w, 500, "搜索日志失败")
			return
		}
		h.Time = at.Format(time.RFC3339)
		out = append(out, h)
	}
	if rows.Err() != nil {
		fail(w, 500, "搜索日志失败")
		return
	}
	more := len(out) > 20
	if more {
		out = out[:20]
	}
	reply(w, 200, map[string]any{"matches": out, "more": more})
}
func (s *Service) createFocus(w http.ResponseWriter, r *http.Request) {
	item, e := idParam(r)
	var v struct {
		Keyword string `json:"keyword"`
		Request string `json:"request"`
	}
	if e != nil || json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&v) != nil {
		fail(w, 400, "参数无效")
		return
	}
	v.Keyword = strings.TrimSpace(v.Keyword)
	if len([]rune(v.Keyword)) < 2 || len([]rune(v.Keyword)) > 80 {
		fail(w, 400, "关键词无效")
		return
	}
	rid, e := uuid.Parse(v.Request)
	if e != nil {
		fail(w, 400, "日志编号无效")
		return
	}
	var jid int64
	e = s.Store.Pool.QueryRow(r.Context(), `insert into wm_focus_jobs(item_id,request_id,keyword,created_by) select $1,r.id,$3,$4 from requests r where r.id=$2 and `+focusScope+` and exists(select 1 from request_texts t where t.request_id=r.id and position(lower($3) in lower(t.text))>0) and not exists(select 1 from wm_focus_jobs f where f.item_id=$1 and f.request_id=r.id and f.keyword=$3 and f.state in ('queued','running','done')) returning id`, item, rid, v.Keyword, who(r).employee.ID).Scan(&jid)
	if e != nil {
		fail(w, 409, "匹配记录不可用，或已有相同的分析任务")
		return
	}
	reply(w, 200, map[string]any{"id": jid})
}
func (s *Service) listFocus(w http.ResponseWriter, r *http.Request) {
	rows, e := s.Store.Pool.Query(r.Context(), `select f.id,f.item_id,f.keyword,f.state,f.result,f.error,f.created_at::text,(select count(*) from wm_focus_chunks c where c.job_id=f.id and c.phase='extract' and c.state='done'),coalesce((select string_agg(c.output,E'\n\n' order by c.ordinal) from wm_focus_chunks c where c.job_id=f.id and c.phase='extract' and c.state='done'),'') from wm_focus_jobs f join wm_knowledge_items i on i.id=f.item_id where `+focusVisible+` order by f.id desc limit 100`)
	if e != nil {
		fail(w, 500, "读取日志深挖失败")
		return
	}
	defer rows.Close()
	type job struct {
		ID, ItemID                               int64
		Keyword, State, Result, Error, CreatedAt string
		Segments                                 int64
		Partial                                  string
	}
	out := []job{}
	for rows.Next() {
		var j job
		if rows.Scan(&j.ID, &j.ItemID, &j.Keyword, &j.State, &j.Result, &j.Error, &j.CreatedAt, &j.Segments, &j.Partial) != nil {
			fail(w, 500, "读取日志深挖失败")
			return
		}
		out = append(out, j)
	}
	if rows.Err() != nil {
		fail(w, 500, "读取日志深挖失败")
		return
	}
	reply(w, 200, map[string]any{"jobs": out})
}
