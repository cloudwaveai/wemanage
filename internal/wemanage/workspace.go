package wemanage

import (
	"net/http"
	"strconv"
	"time"
)

type workspaceRow struct {
	Hour       string   `json:"hour"`
	EmployeeID int64    `json:"employee_id"`
	Employee   string   `json:"employee"`
	Service    bool     `json:"service"`
	ProjectID  int64    `json:"project_id"`
	Project    string   `json:"project"`
	Category   string   `json:"category"`
	Model      string   `json:"model"`
	Requests   int64    `json:"requests"`
	Input      int64    `json:"input"`
	Cached     int64    `json:"cached"`
	Output     int64    `json:"output"`
	Reasoning  int64    `json:"reasoning"`
	Errors     int64    `json:"errors"`
	Currency   string   `json:"currency"`
	Priced     int64    `json:"priced"`
	Cost       *float64 `json:"cost"`
}
type workspacePerson struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Service    bool       `json:"service"`
	Requests   int64      `json:"requests"`
	Sessions   int64      `json:"sessions"`
	ActiveDays int64      `json:"active_days"`
	Imported   int64      `json:"imported"`
	Last       *time.Time `json:"last"`
}
type workspaceDigest struct {
	Day      string `json:"day"`
	Text     string `json:"text"`
	Sessions int64  `json:"sessions"`
	Messages int64  `json:"messages"`
	Model    string `json:"model"`
}
type workspaceClue struct {
	Category  string `json:"category"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Project   string `json:"project"`
	From      string `json:"from"`
	SourceDay string `json:"source_day"`
	Quote     string `json:"quote"`
	Verified  bool   `json:"verified"`
}

// workspaceStats applies the shared-analysis privacy rules before every aggregate.
func (s *Service) workspaceStats(w http.ResponseWriter, r *http.Request) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		fail(w, 500, "时区不可用")
		return
	}
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	from, to := today.AddDate(0, 0, -6), now
	label := "7d"
	switch r.URL.Query().Get("range") {
	case "today":
		from = today
		label = "today"
	case "yesterday":
		from = today.AddDate(0, 0, -1)
		to = today
		label = "yesterday"
	case "30d":
		from = today.AddDate(0, 0, -29)
		label = "30d"
	case "", "7d":
	default:
		fail(w, 400, "日期范围无效")
		return
	}
	if month := r.URL.Query().Get("month"); month != "" {
		if r.URL.Query().Get("day") != "" {
			fail(w, 400, "日期筛选不能同时指定月份和日期")
			return
		}
		first, e := time.ParseInLocation("2006-01", month, loc)
		if e != nil || first.Format("2006-01") != month || first.After(today) {
			fail(w, 400, "月份无效")
			return
		}
		from = first
		to = first.AddDate(0, 1, 0)
		if to.After(now) {
			to = now
		}
		label = "month"
	}
	if day := r.URL.Query().Get("day"); day != "" {
		d, e := time.ParseInLocation(time.DateOnly, day, loc)
		if e != nil || d.After(today) {
			fail(w, 400, "日期无效")
			return
		}
		from = d
		to = d.AddDate(0, 0, 1)
		if to.After(now) {
			to = now
		}
		label = "day"
	}
	var id int64
	if text := r.URL.Query().Get("employee_id"); text != "" {
		id, err = strconv.ParseInt(text, 10, 64)
		if err != nil || id <= 0 {
			fail(w, 400, "人员无效")
			return
		}
		var ok bool
		err = s.Store.Pool.QueryRow(r.Context(), `select exists(select 1 from employees e where e.id=$1 and `+humanVisible+`)`, id).Scan(&ok)
		if err != nil || !ok {
			fail(w, 404, "人员不存在或无权查看")
			return
		}
	}
	// A project marked hidden is excluded from all aggregate views as well.
	const joins = ` from requests r join employees e on e.id=r.employee_id left join api_keys k on k.id=r.api_key_id left join request_projects rp on rp.request_id=r.id left join projects p on p.id=coalesce(rp.project_id,k.project_id)`
	const filter = ` where r.started_at >= $1 and r.started_at < $2 and e.private_work_since is null and e.name<>'林伊默' and ($3::bigint=0 or e.id=$3) and not exists(select 1 from wm_hidden_projects h where h.project_id=p.id)`
	rows, err := s.Store.Pool.Query(r.Context(), `select date_trunc('hour',r.started_at at time zone 'Asia/Shanghai'),e.id,e.name,e.is_service,coalesce(p.id,0),coalesce(p.name,'未归属项目'),coalesce(p.category,''),coalesce(nullif(r.model_requested,''),'未记录'),count(*),coalesce(sum(r.tokens_input),0),coalesce(sum(r.tokens_cached),0),coalesce(sum(r.tokens_output),0),coalesce(sum(r.tokens_reasoning),0),count(*) filter(where r.status_code>=400),coalesce(rate.currency,''),count(*) filter(where rate.origin is not null),sum((greatest(r.tokens_input-r.tokens_cached,0)*rate.input_per_million+r.tokens_cached*rate.cached_per_million+r.tokens_output*rate.output_per_million)/1000000)::float8`+joins+` `+priceJoin+` `+filter+` group by 1,2,3,4,5,6,7,8,15 order by 1,3,6,8,15`, from, to, id)
	if err != nil {
		fail(w, 500, "读取用量统计失败")
		return
	}
	out := []workspaceRow{}
	for rows.Next() {
		var x workspaceRow
		var hour time.Time
		if rows.Scan(&hour, &x.EmployeeID, &x.Employee, &x.Service, &x.ProjectID, &x.Project, &x.Category, &x.Model, &x.Requests, &x.Input, &x.Cached, &x.Output, &x.Reasoning, &x.Errors, &x.Currency, &x.Priced, &x.Cost) != nil {
			rows.Close()
			fail(w, 500, "读取用量统计失败")
			return
		}
		x.Hour = hour.Format("2006-01-02 15:04")
		out = append(out, x)
	}
	if rows.Err() != nil {
		rows.Close()
		fail(w, 500, "读取用量统计失败")
		return
	}
	rows.Close()
	rows, err = s.Store.Pool.Query(r.Context(), `select e.id,e.name,e.is_service,count(*),count(distinct r.session_id),count(distinct (r.started_at at time zone 'Asia/Shanghai')::date),count(*) filter(where r.endpoint='/local/codex-import'),max(r.started_at)`+joins+filter+` and `+humanVisible+` group by e.id,e.name,e.is_service order by e.name,e.id`, from, to, id)
	if err != nil {
		fail(w, 500, "读取人员统计失败")
		return
	}
	people := []workspacePerson{}
	for rows.Next() {
		var p workspacePerson
		if rows.Scan(&p.ID, &p.Name, &p.Service, &p.Requests, &p.Sessions, &p.ActiveDays, &p.Imported, &p.Last) != nil {
			rows.Close()
			fail(w, 500, "读取人员统计失败")
			return
		}
		people = append(people, p)
	}
	if rows.Err() != nil {
		rows.Close()
		fail(w, 500, "读取人员统计失败")
		return
	}
	rows.Close()
	var withText, purged int64
	err = s.Store.Pool.QueryRow(r.Context(), `select count(*) filter(where exists(select 1 from request_texts textrow where textrow.request_id=r.id)),count(*) filter(where r.bodies_purged)`+joins+filter, from, to, id).Scan(&withText, &purged)
	if err != nil {
		fail(w, 500, "读取采集覆盖失败")
		return
	}
	digests := []workspaceDigest{}
	clues := []workspaceClue{}
	if id > 0 {
		rows, err = s.Store.Pool.Query(r.Context(), `select d.day::text,d.summary_md,d.sessions,d.messages,d.model from work_digests d join work_digest_days day on day.day=d.day and day.status='done' join employees e on e.id=d.employee_id where d.employee_id=$1 and e.private_work_since is null and e.name<>'林伊默' and d.day >= ($2::timestamptz at time zone 'Asia/Shanghai')::date and d.day <= (($3::timestamptz - interval '1 microsecond') at time zone 'Asia/Shanghai')::date order by d.day desc`, id, from, to)
		if err != nil {
			fail(w, 500, "读取人员日报失败")
			return
		}
		for rows.Next() {
			var d workspaceDigest
			if rows.Scan(&d.Day, &d.Text, &d.Sessions, &d.Messages, &d.Model) != nil {
				rows.Close()
				fail(w, 500, "读取人员日报失败")
				return
			}
			digests = append(digests, d)
		}
		if rows.Err() != nil {
			rows.Close()
			fail(w, 500, "读取人员日报失败")
			return
		}
		rows.Close()
		rows, err = s.Store.Pool.Query(r.Context(), `select distinct i.category,coalesce(i.reviewed_title,i.title),coalesce(i.reviewed_content,i.content),i.project_hint,run.period_start::text,src.source_day::text,coalesce((select string_agg(x->>'quote',E'\n') from jsonb_array_elements(i.evidence) x where (x->>'source')::int=src.ordinal),''),i.evidence_verified from wm_knowledge_items i join wm_knowledge_runs run on run.id=i.run_id join wm_knowledge_sources src on src.run_id=run.id join employees e on e.id=src.employee_id where src.employee_id=$1 and src.source_day >= ($2::timestamptz at time zone 'Asia/Shanghai')::date and src.source_day <= (($3::timestamptz-interval '1 microsecond') at time zone 'Asia/Shanghai')::date and e.private_work_since is null and e.name<>'林伊默' and i.status<>'rejected' and (i.reviewed_by is null or exists(select 1 from employees reviewer where reviewer.id=i.reviewed_by and reviewer.private_work_since is null and reviewer.name<>'林伊默')) and exists(select 1 from jsonb_array_elements(i.evidence) x where (x->>'source')::int=src.ordinal) and not exists(select 1 from wm_knowledge_sources hidden join employees ep on ep.id=hidden.employee_id where hidden.run_id=run.id and (ep.private_work_since is not null or ep.name='林伊默')) order by run.period_start::text desc,2`, id, from, to)
		if err != nil {
			fail(w, 500, "读取人员知识线索失败")
			return
		}
		for rows.Next() {
			var c workspaceClue
			if rows.Scan(&c.Category, &c.Title, &c.Content, &c.Project, &c.From, &c.SourceDay, &c.Quote, &c.Verified) != nil {
				rows.Close()
				fail(w, 500, "读取人员知识线索失败")
				return
			}
			clues = append(clues, c)
		}
		if rows.Err() != nil {
			rows.Close()
			fail(w, 500, "读取人员知识线索失败")
			return
		}
		rows.Close()
	}
	reply(w, 200, map[string]any{"range": label, "from": from.Format(time.RFC3339), "to": to.Format(time.RFC3339), "timezone": "Asia/Shanghai", "rows": out, "people": people, "digests": digests, "clues": clues, "with_text": withText, "purged": purged})
}
