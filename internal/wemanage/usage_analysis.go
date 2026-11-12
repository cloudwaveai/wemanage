package wemanage

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// All diagnostics inherit shared-work privacy, including hidden projects.
const diagnosticJoins = ` from requests r join employees e on e.id=r.employee_id left join api_keys k on k.id=r.api_key_id left join request_projects rp on rp.request_id=r.id`
const diagnosticVisible = ` e.private_work_since is null and e.name<>'林伊默' and not exists(select 1 from wm_hidden_projects h where h.project_id=coalesce(rp.project_id,k.project_id))`
const diagnosticFilter = ` where r.started_at >= $1 and r.started_at < $2 and ($3::bigint=0 or e.id=$3) and ` + diagnosticVisible

type diagnosticRange struct {
	From, To, PreviousFrom, PreviousTo time.Time
	Employee                           int64
}

func parseDiagnosticRange(r *http.Request, now time.Time) (diagnosticRange, error) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return diagnosticRange{}, err
	}
	now = now.In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	from, to := today.AddDate(0, 0, -1), today.AddDate(0, 0, 1)
	q := r.URL.Query()
	for _, field := range []struct {
		name   string
		target *time.Time
	}{{"from", &from}, {"to", &to}} {
		if v := q.Get(field.name); v != "" {
			d, e := time.ParseInLocation(time.DateOnly, v, loc)
			if e != nil {
				return diagnosticRange{}, fmt.Errorf("日期无效")
			}
			if field.name == "to" {
				d = d.AddDate(0, 0, 1)
			}
			*field.target = d
		}
	}
	if !to.After(from) || to.Sub(from) > 31*24*time.Hour || from.After(today) || to.After(today.AddDate(0, 0, 1)) {
		return diagnosticRange{}, fmt.Errorf("请选择不超过31天的有效日期")
	}
	span := to.Sub(from)
	actualTo := to
	if actualTo.After(now) {
		actualTo = now
	}
	id := int64(0)
	if v := q.Get("employee_id"); v != "" {
		id, err = strconv.ParseInt(v, 10, 64)
		if err != nil || id < 1 {
			return diagnosticRange{}, fmt.Errorf("人员无效")
		}
	}
	return diagnosticRange{from, actualTo, from.Add(-span), actualTo.Add(-span), id}, nil
}
func (s *Service) diagnosticRange(w http.ResponseWriter, r *http.Request) (diagnosticRange, bool) {
	d, e := parseDiagnosticRange(r, time.Now())
	if e != nil {
		fail(w, 400, e.Error())
		return d, false
	}
	if d.Employee > 0 {
		var ok bool
		e = s.Store.Pool.QueryRow(r.Context(), `select exists(select 1 from employees where id=$1 and private_work_since is null and name<>'林伊默')`, d.Employee).Scan(&ok)
		if e != nil {
			fail(w, 500, "读取人员失败")
			return d, false
		}
		if !ok {
			fail(w, 404, "人员不存在或无权查看")
			return d, false
		}
	}
	return d, true
}

type diagnosticMetric struct {
	Kind        string     `json:"kind"`
	Key         string     `json:"key"`
	EmployeeID  int64      `json:"employee_id"`
	Employee    string     `json:"employee"`
	Requests    int64      `json:"requests"`
	Input       int64      `json:"input"`
	Cached      int64      `json:"cached"`
	Output      int64      `json:"output"`
	Reasoning   int64      `json:"reasoning"`
	Errors      int64      `json:"errors"`
	ErrorTokens int64      `json:"error_tokens"`
	Uncached    int64      `json:"uncached"`
	MaxInput    int64      `json:"max_input"`
	First       *time.Time `json:"first"`
	Last        *time.Time `json:"last"`
}

const diagnosticSums = `count(*),coalesce(sum(tokens_input),0),coalesce(sum(tokens_cached),0),coalesce(sum(tokens_output),0),coalesce(sum(tokens_reasoning),0),count(*) filter(where status_code>=400 or coalesce(error_type,'')<>''),coalesce(sum(coalesce(tokens_input,0)+coalesce(tokens_output,0)) filter(where status_code>=400 or coalesce(error_type,'')<>''),0),coalesce(sum(greatest(coalesce(tokens_input,0)-coalesce(tokens_cached,0),0)),0),coalesce(max(tokens_input),0),min(started_at),max(started_at)`

func (s *Service) usageAnalysis(w http.ResponseWriter, r *http.Request) {
	d, ok := s.diagnosticRange(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	tx, err := s.Store.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		fail(w, 500, "读取用量失败")
		return
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `with base as(select r.*,e.name as employee_name,to_char(r.started_at at time zone 'Asia/Shanghai','YYYY-MM-DD') as day,to_char(r.started_at at time zone 'Asia/Shanghai','YYYY-MM-DD HH24:00') as hour `+diagnosticJoins+diagnosticFilter+`) select case when grouping(session_id)=0 then 'session' when grouping(employee_id)=0 then 'person' when grouping(model_requested)=0 then 'model' when grouping(hour)=0 then 'hour' when grouping(day)=0 then 'day' else 'total' end,case when grouping(session_id)=0 then coalesce(session_id::text,'0') when grouping(employee_id)=0 then employee_id::text when grouping(model_requested)=0 then coalesce(nullif(model_requested,''),'未记录') when grouping(hour)=0 then hour when grouping(day)=0 then day else 'total' end,coalesce(employee_id,0),coalesce(employee_name,''),`+diagnosticSums+` from base group by grouping sets((),(day),(hour),(model_requested),(employee_id,employee_name),(session_id,employee_id,employee_name)) order by 1,5 desc,2`, d.From, d.To, d.Employee)
	if err != nil {
		fail(w, 500, "读取用量失败")
		return
	}
	out := []diagnosticMetric{}
	for rows.Next() {
		var x diagnosticMetric
		if err = rows.Scan(&x.Kind, &x.Key, &x.EmployeeID, &x.Employee, &x.Requests, &x.Input, &x.Cached, &x.Output, &x.Reasoning, &x.Errors, &x.ErrorTokens, &x.Uncached, &x.MaxInput, &x.First, &x.Last); err != nil {
			break
		}
		out = append(out, x)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		fail(w, 500, "读取用量失败")
		return
	}
	var previous diagnosticMetric
	err = tx.QueryRow(ctx, `select `+diagnosticSums+diagnosticJoins+diagnosticFilter, d.PreviousFrom, d.PreviousTo, d.Employee).Scan(&previous.Requests, &previous.Input, &previous.Cached, &previous.Output, &previous.Reasoning, &previous.Errors, &previous.ErrorTokens, &previous.Uncached, &previous.MaxInput, &previous.First, &previous.Last)
	if err != nil {
		fail(w, 500, "读取对照用量失败")
		return
	}
	people := []map[string]any{}
	pr, err := tx.Query(ctx, `select id,name from employees where private_work_since is null and name<>'林伊默' order by name,id`)
	if err != nil {
		fail(w, 500, "读取人员失败")
		return
	}
	for pr.Next() {
		var id int64
		var name string
		if err = pr.Scan(&id, &name); err != nil {
			break
		}
		people = append(people, map[string]any{"id": id, "name": name})
	}
	if err == nil {
		err = pr.Err()
	}
	pr.Close()
	if err != nil {
		fail(w, 500, "读取人员失败")
		return
	}
	reply(w, 200, map[string]any{"from": d.From, "to": d.To, "previous_from": d.PreviousFrom, "previous_to": d.PreviousTo, "rows": out, "previous": previous, "people": people, "timezone": "Asia/Shanghai"})
}
func parseCursor(r *http.Request) (int64, error) {
	v := r.URL.Query().Get("cursor")
	if v == "" {
		return 0, nil
	}
	n, e := strconv.ParseInt(v, 10, 64)
	if e != nil || n < 1 {
		return 0, fmt.Errorf("分页参数无效")
	}
	return n, nil
}
func (s *Service) usageEvidence(w http.ResponseWriter, r *http.Request) {
	d, ok := s.diagnosticRange(w, r)
	if !ok {
		return
	}
	sid, e := strconv.ParseInt(r.URL.Query().Get("session_id"), 10, 64)
	cursor, ce := parseCursor(r)
	if e != nil || sid < 1 || d.Employee < 1 || ce != nil {
		fail(w, 400, "请选择人员与有效会话")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	// Apply visibility to each source request, not just the session owner.
	rows, err := s.Store.Pool.Query(ctx, `select t.id,t.request_id::text,t.started_at,t.role,t.text from request_texts t join requests r on r.id=t.request_id join employees e on e.id=r.employee_id left join api_keys k on k.id=r.api_key_id left join request_projects rp on rp.request_id=r.id where r.started_at >= $1 and r.started_at < $2 and e.id=$3 and r.session_id=$4 and t.id>$5 and t.employee_id=r.employee_id and `+diagnosticVisible+` order by t.id limit 51`, d.From, d.To, d.Employee, sid, cursor)
	if err != nil {
		fail(w, 500, "读取会话证据失败")
		return
	}
	defer rows.Close()
	type evidence struct {
		ID      int64     `json:"id"`
		Request string    `json:"request"`
		At      time.Time `json:"at"`
		Role    string    `json:"role"`
		Text    string    `json:"text"`
	}
	items := []evidence{}
	for rows.Next() {
		var x evidence
		if rows.Scan(&x.ID, &x.Request, &x.At, &x.Role, &x.Text) != nil {
			fail(w, 500, "读取会话证据失败")
			return
		}
		items = append(items, x)
	}
	if rows.Err() != nil {
		fail(w, 500, "读取会话证据失败")
		return
	}
	next := int64(0)
	if len(items) > 50 {
		items = items[:50]
		next = items[49].ID
	}
	reply(w, 200, map[string]any{"items": items, "next_cursor": next, "note": "网关提取文本每条最多保存8000字；user角色可能包含代理交接和历史上下文，不等同于本人输入。分页保留全部已采集条目。"})
}

// A phrase collision is only a review hint. It never resolves an event or
// disables the gateway rule; a stored snippet is not the complete request.
func riskHint(rule, snippet string) string {
	if rule == "av-evasion" && strings.Contains(snippet, "避免杀") {
		return "疑似词组误报：“避免杀…”包含关键词“免杀”。请结合原始请求核查；此提示不代表已审核。"
	}
	return "规则命中不等于实际违规，请结合上下文核查。"
}
func (s *Service) riskAnalysis(w http.ResponseWriter, r *http.Request) {
	d, ok := s.diagnosticRange(w, r)
	if !ok {
		return
	}
	cursor, err := parseCursor(r)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	// Events have no request FK. Conservatively withhold events for anyone with
	// hidden-project activity, rather than disclose an unscoped content snippet.
	rows, err := s.Store.Pool.Query(ctx, `select v.id,v.at,v.employee_id,e.name,v.rule,v.severity,coalesce(v.detail->>'rule',''),coalesce(v.detail->>'snippet',''),coalesce(v.detail->>'blocked','false')='true',v.resolved_at from risk_events v join employees e on e.id=v.employee_id where v.at >= $1 and v.at < $2 and ($3::bigint=0 or e.id=$3) and ($4::bigint=0 or v.id<$4) and e.private_work_since is null and e.name<>'林伊默' and not exists(select 1 from requests r left join api_keys k on k.id=r.api_key_id left join request_projects rp on rp.request_id=r.id join wm_hidden_projects h on h.project_id=coalesce(rp.project_id,k.project_id) where r.employee_id=e.id) order by v.id desc limit 101`, d.From, d.To, d.Employee, cursor)
	if err != nil {
		fail(w, 500, "读取风险记录失败")
		return
	}
	defer rows.Close()
	type event struct {
		ID         int64      `json:"id"`
		At         time.Time  `json:"at"`
		EmployeeID int64      `json:"employee_id"`
		Employee   string     `json:"employee"`
		Kind       string     `json:"kind"`
		Severity   string     `json:"severity"`
		Rule       string     `json:"rule"`
		Snippet    string     `json:"snippet"`
		Blocked    bool       `json:"blocked"`
		Resolved   *time.Time `json:"resolved_at"`
		Hint       string     `json:"hint"`
		Matched    string     `json:"matched"`
	}
	out := []event{}
	for rows.Next() {
		var x event
		if rows.Scan(&x.ID, &x.At, &x.EmployeeID, &x.Employee, &x.Kind, &x.Severity, &x.Rule, &x.Snippet, &x.Blocked, &x.Resolved) != nil {
			fail(w, 500, "读取风险记录失败")
			return
		}
		if x.Kind == "content_flagged" {
			x.Hint = riskHint(x.Rule, x.Snippet)
			if x.Rule == "av-evasion" && strings.Contains(x.Snippet, "免杀") {
				x.Matched = "免杀"
			}
		}
		out = append(out, x)
	}
	if rows.Err() != nil {
		fail(w, 500, "读取风险记录失败")
		return
	}
	next := int64(0)
	if len(out) > 100 {
		out = out[:100]
		next = out[99].ID
	}
	reply(w, 200, map[string]any{"events": out, "next_cursor": next, "from": d.From, "to": d.To, "note": "告警片段为命中时保存的摘录，不是完整请求；风险记录按规则去重，不代表违规或命中请求次数。涉及私密人员或隐藏项目的记录不展示。"})
}
