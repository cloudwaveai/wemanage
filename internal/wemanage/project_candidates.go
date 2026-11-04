package wemanage

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"
)

type projectClue struct {
	ID       int64           `json:"id"`
	Title    string          `json:"title"`
	Content  string          `json:"content"`
	Category string          `json:"category"`
	Status   string          `json:"status"`
	From     string          `json:"from"`
	To       string          `json:"to"`
	Sources  json.RawMessage `json:"sources"`
}
type projectCandidate struct {
	Name    string        `json:"name"`
	Aliases []string      `json:"aliases"`
	Hidden  bool          `json:"hidden"`
	Clues   []projectClue `json:"clues"`
}

// The project candidates use already generated daily/weekly extraction, not
// gateway project IDs. Exclude whole runs whose sources or reviewers are private.
const projectClueVisible = `not exists(select 1 from wm_knowledge_sources src join employees e on e.id=src.employee_id where src.run_id=i.run_id and (e.private_work_since is not null or e.name='林伊默' or exists(select 1 from requests r left join api_keys k on k.id=r.api_key_id left join request_projects rp on rp.request_id=r.id join wm_hidden_projects hp on hp.project_id=coalesce(rp.project_id,k.project_id) where r.employee_id=e.id and (r.started_at at time zone 'Asia/Shanghai')::date=src.source_day))) and (i.reviewed_by is null or exists(select 1 from employees reviewer where reviewer.id=i.reviewed_by and reviewer.private_work_since is null and reviewer.name<>'林伊默')) and not exists(select 1 from projects hp join wm_hidden_projects h on h.project_id=hp.id where hp.name=i.project_hint)`

func (s *Service) projectCandidates(w http.ResponseWriter, r *http.Request) {
	aliases := map[string]string{}
	rows, err := s.Store.Pool.Query(r.Context(), `select alias,canonical from wm_project_candidate_aliases`)
	if err != nil {
		fail(w, 500, "读取项目归并失败")
		return
	}
	for rows.Next() {
		var a, c string
		if rows.Scan(&a, &c) != nil {
			rows.Close()
			fail(w, 500, "读取项目归并失败")
			return
		}
		aliases[a] = c
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, 500, "读取项目归并失败")
		return
	}
	hidden := map[string]bool{}
	rows, err = s.Store.Pool.Query(r.Context(), `select name,hidden from wm_project_candidate_states`)
	if err != nil {
		fail(w, 500, "读取项目状态失败")
		return
	}
	for rows.Next() {
		var n string
		var h bool
		if rows.Scan(&n, &h) != nil {
			rows.Close()
			fail(w, 500, "读取项目状态失败")
			return
		}
		hidden[n] = h
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		fail(w, 500, "读取项目状态失败")
		return
	}
	rows, err = s.Store.Pool.Query(r.Context(), `select i.id,trim(i.project_hint),coalesce(i.reviewed_title,i.title),coalesce(i.reviewed_content,i.content),i.category,i.status,run.period_start::text,run.period_end::text,coalesce((select jsonb_agg(jsonb_build_object('day',src.source_day,'employee_id',src.employee_id,'name',src.employee_name,'quote',ev->>'quote','verified',position(coalesce(ev->>'quote','') in src.summary_md)>0 and length(coalesce(ev->>'quote',''))>0) order by src.ordinal) from jsonb_array_elements(i.evidence) ev join wm_knowledge_sources src on src.run_id=i.run_id and src.ordinal=case when (ev->>'source') ~ '^[0-9]+$' then (ev->>'source')::int else -1 end),'[]'::jsonb) from wm_knowledge_items i join wm_knowledge_runs run on run.id=i.run_id where run.status='done' and i.status<>'rejected' and length(trim(i.project_hint))>0 and exists(select 1 from jsonb_array_elements(i.evidence) ev join wm_knowledge_sources src on src.run_id=i.run_id and src.ordinal=case when (ev->>'source') ~ '^[0-9]+$' then (ev->>'source')::int else -1 end) and `+projectClueVisible+` order by run.period_start desc,i.id desc`)
	if err != nil {
		fail(w, 500, "读取项目线索失败")
		return
	}
	defer rows.Close()
	by := map[string]*projectCandidate{}
	for rows.Next() {
		var name string
		var c projectClue
		if rows.Scan(&c.ID, &name, &c.Title, &c.Content, &c.Category, &c.Status, &c.From, &c.To, &c.Sources) != nil {
			fail(w, 500, "读取项目线索失败")
			return
		}
		canon := name
		if v := aliases[name]; v != "" {
			canon = v
		}
		g := by[canon]
		if g == nil {
			g = &projectCandidate{Name: canon, Aliases: []string{}, Hidden: hidden[canon], Clues: []projectClue{}}
			by[canon] = g
		}
		if name != canon {
			found := false
			for _, a := range g.Aliases {
				if a == name {
					found = true
				}
			}
			if !found {
				g.Aliases = append(g.Aliases, name)
			}
		}
		g.Clues = append(g.Clues, c)
	}
	if rows.Err() != nil {
		fail(w, 500, "读取项目线索失败")
		return
	}
	out := make([]*projectCandidate, 0, len(by))
	for _, g := range by {
		sort.Strings(g.Aliases)
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	reply(w, 200, map[string]any{"projects": out})
}
func (s *Service) updateProjectCandidate(w http.ResponseWriter, r *http.Request) {
	var v struct {
		Action string `json:"action"`
		Name   string `json:"name"`
		Target string `json:"target"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&v) != nil {
		fail(w, 400, "请求无效")
		return
	}
	v.Name = strings.TrimSpace(v.Name)
	v.Target = strings.TrimSpace(v.Target)
	if v.Name == "" || utf8.RuneCountInString(v.Name) > 150 || utf8.RuneCountInString(v.Target) > 150 {
		fail(w, 400, "项目名称无效")
		return
	}
	tx, err := s.Store.Pool.Begin(r.Context())
	if err != nil {
		fail(w, 500, "保存失败")
		return
	}
	defer tx.Rollback(r.Context())
	switch v.Action {
	case "hide", "restore":
		_, err = tx.Exec(r.Context(), `insert into wm_project_candidate_states(name,hidden,updated_by) values($1,$2,$3) on conflict(name) do update set hidden=excluded.hidden,updated_by=excluded.updated_by,updated_at=now()`, v.Name, v.Action == "hide", who(r).employee.ID)
	case "merge":
		if v.Target == "" || v.Target == v.Name {
			fail(w, 400, "请选择另一个项目")
			return
		}
		var target string
		err = tx.QueryRow(r.Context(), `select coalesce((select canonical from wm_project_candidate_aliases where alias=$1),$1)`, v.Target).Scan(&target)
		if err == nil && target == v.Name {
			fail(w, 400, "不能合并到自身")
			return
		}
		if err == nil {
			_, err = tx.Exec(r.Context(), `update wm_project_candidate_aliases set canonical=$2,updated_by=$3,updated_at=now() where canonical=$1`, v.Name, target, who(r).employee.ID)
		}
		if err == nil {
			_, err = tx.Exec(r.Context(), `insert into wm_project_candidate_aliases(alias,canonical,updated_by) values($1,$2,$3) on conflict(alias) do update set canonical=excluded.canonical,updated_by=excluded.updated_by,updated_at=now()`, v.Name, target, who(r).employee.ID)
		}
	default:
		fail(w, 400, "操作无效")
		return
	}
	if err != nil || tx.Commit(r.Context()) != nil {
		fail(w, 500, "保存失败")
		return
	}
	reply(w, 200, map[string]bool{"saved": true})
}
