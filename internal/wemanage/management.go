package wemanage

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const humanVisible = `e.status='active' and not e.is_service and nullif(e.feishu_open_id,'') is not null and e.private_work_since is null and e.name<>'林伊默'`
const expenseVisible = `not exists(select 1 from wm_expense_versions pv join employees pe on pe.id=pv.editor_id or pe.id=any(pv.employee_ids) where pv.expense_id=v.expense_id and (pe.private_work_since is not null or pe.name='林伊默'))`
const managementSchema = `
CREATE TABLE IF NOT EXISTS wm_expenses(id BIGSERIAL PRIMARY KEY,created_by BIGINT NOT NULL REFERENCES employees(id));
CREATE TABLE IF NOT EXISTS wm_expense_versions(id BIGSERIAL PRIMARY KEY,expense_id BIGINT NOT NULL REFERENCES wm_expenses(id),version INT NOT NULL,month DATE NOT NULL,kind TEXT NOT NULL,currency TEXT NOT NULL,amount NUMERIC(18,2) NOT NULL CHECK(amount>=0),label TEXT NOT NULL,account_label TEXT NOT NULL,seat_count INT NOT NULL CHECK(seat_count>=0),employee_ids BIGINT[] NOT NULL,receipt TEXT NOT NULL,note TEXT NOT NULL,hidden BOOLEAN NOT NULL DEFAULT false,editor_id BIGINT NOT NULL REFERENCES employees(id),created_at TIMESTAMPTZ NOT NULL DEFAULT now(),UNIQUE(expense_id,version));
CREATE TABLE IF NOT EXISTS wm_managed_projects(id BIGSERIAL PRIMARY KEY,name TEXT NOT NULL UNIQUE,owner_id BIGINT REFERENCES employees(id),status TEXT NOT NULL DEFAULT 'active',start_day DATE,acceptance_day DATE,payments JSONB NOT NULL DEFAULT '[]',version INT NOT NULL DEFAULT 1,updated_by BIGINT NOT NULL REFERENCES employees(id),updated_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS wm_managed_project_history(id BIGSERIAL PRIMARY KEY,project_id BIGINT NOT NULL REFERENCES wm_managed_projects(id),snapshot JSONB NOT NULL,editor_id BIGINT NOT NULL REFERENCES employees(id),created_at TIMESTAMPTZ NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS wm_asset_review_tasks(id BIGSERIAL PRIMARY KEY,asset_id BIGINT NOT NULL REFERENCES wm_assets(id),version INT NOT NULL,reviewer_id BIGINT NOT NULL REFERENCES employees(id),assigned_by BIGINT NOT NULL REFERENCES employees(id),state TEXT NOT NULL DEFAULT 'pending',note TEXT NOT NULL DEFAULT '',updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),UNIQUE(asset_id,version,reviewer_id));
CREATE TABLE IF NOT EXISTS wm_review_task_history(id BIGSERIAL PRIMARY KEY,task_id BIGINT NOT NULL REFERENCES wm_asset_review_tasks(id),action TEXT NOT NULL,note TEXT NOT NULL,actor_id BIGINT NOT NULL REFERENCES employees(id),created_at TIMESTAMPTZ NOT NULL DEFAULT now());`

var moneyPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,11})(\.[0-9]{1,2})?$`)

func validDay(s string) bool {
	if s == "" {
		return true
	}
	d, e := time.Parse(time.DateOnly, s)
	return e == nil && d.Format(time.DateOnly) == s
}
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func (s *Service) expenses(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Pool.Query(r.Context(), `select a.id,v.version,to_char(v.month,'YYYY-MM'),v.kind,v.currency,v.amount::text,v.label,v.account_label,v.seat_count,v.employee_ids,v.receipt,v.note,v.hidden,v.created_at from wm_expenses a join lateral(select * from wm_expense_versions where expense_id=a.id order by version desc limit 1) v on true where `+expenseVisible+` order by v.month desc,a.id desc`)
	if err != nil {
		fail(w, 500, "读取实际费用失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var ver, seats int
		var month, kind, currency, amount, label, account, receipt, note string
		var ids []int64
		var hidden bool
		var at time.Time
		if rows.Scan(&id, &ver, &month, &kind, &currency, &amount, &label, &account, &seats, &ids, &receipt, &note, &hidden, &at) != nil {
			fail(w, 500, "读取费用失败")
			return
		}
		out = append(out, map[string]any{"id": id, "version": ver, "month": month, "kind": kind, "currency": currency, "amount": amount, "label": label, "account_label": account, "seat_count": seats, "employee_ids": ids, "receipt": receipt, "note": note, "hidden": hidden, "updated_at": at})
	}
	if rows.Err() != nil {
		fail(w, 500, "读取费用失败")
		return
	}
	reply(w, 200, map[string]any{"entries": out})
}
func (s *Service) saveExpense(w http.ResponseWriter, r *http.Request) {
	if !s.managementWriter(w, r) {
		return
	}
	var q struct {
		ID       int64   `json:"id"`
		Version  int     `json:"version"`
		Month    string  `json:"month"`
		Kind     string  `json:"kind"`
		Currency string  `json:"currency"`
		Amount   string  `json:"amount"`
		Label    string  `json:"label"`
		Account  string  `json:"account_label"`
		Seats    int     `json:"seat_count"`
		People   []int64 `json:"employee_ids"`
		Receipt  string  `json:"receipt"`
		Note     string  `json:"note"`
		Hidden   bool    `json:"hidden"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64000)).Decode(&q) != nil {
		fail(w, 400, "费用格式无效")
		return
	}
	m, e := time.Parse("2006-01", q.Month)
	if e != nil || m.Format("2006-01") != q.Month || !moneyPattern.MatchString(q.Amount) || strings.TrimSpace(q.Label) == "" || len(q.Label) > 500 || q.Seats < 0 || q.Seats > 100000 || q.ID < 0 || q.Version < 0 || (q.Kind != "subscription" && q.Kind != "api") || (q.Currency != "CNY" && q.Currency != "USD" && q.Currency != "EUR" && q.Currency != "HKD") {
		fail(w, 400, "检查月份、费用类型、币种、金额和席位数")
		return
	}
	if q.People == nil {
		q.People = []int64{}
	}
	seen := map[int64]bool{}
	for _, id := range q.People {
		if id <= 0 || seen[id] {
			fail(w, 400, "人员不可重复")
			return
		}
		seen[id] = true
	}
	if q.Kind == "subscription" && len(q.People) > q.Seats {
		fail(w, 400, "关联人数不能超过订阅席位数")
		return
	}
	tx, e := s.Store.Pool.Begin(r.Context())
	if e != nil {
		fail(w, 500, "保存失败")
		return
	}
	defer tx.Rollback(r.Context())
	var count int
	if e = tx.QueryRow(r.Context(), `select count(*) from employees e where e.id=any($1::bigint[]) and `+humanVisible, q.People).Scan(&count); e != nil || count != len(q.People) {
		fail(w, 400, "关联人员必须是可见飞书成员")
		return
	}
	if q.ID == 0 {
		if q.Version != 0 {
			fail(w, 409, "新费用版本应为零")
			return
		}
		e = tx.QueryRow(r.Context(), `insert into wm_expenses(created_by)values($1)returning id`, who(r).employee.ID).Scan(&q.ID)
	} else {
		var id int64
		e = tx.QueryRow(r.Context(), `select id from wm_expenses where id=$1 for update`, q.ID).Scan(&id)
	}
	if e != nil {
		fail(w, 404, "费用不存在")
		return
	}
	var version int
	e = tx.QueryRow(r.Context(), `select coalesce(max(version),0) from wm_expense_versions where expense_id=$1`, q.ID).Scan(&version)
	if e != nil || version != q.Version {
		fail(w, 409, "费用已更新，请刷新")
		return
	}
	_, e = tx.Exec(r.Context(), `insert into wm_expense_versions(expense_id,version,month,kind,currency,amount,label,account_label,seat_count,employee_ids,receipt,note,hidden,editor_id) values($1,$2,$3,$4,$5,$6::numeric,$7,$8,$9,$10,$11,$12,$13,$14)`, q.ID, version+1, m, q.Kind, q.Currency, q.Amount, strings.TrimSpace(q.Label), q.Account, q.Seats, q.People, q.Receipt, q.Note, q.Hidden, who(r).employee.ID)
	if e != nil || tx.Commit(r.Context()) != nil {
		fail(w, 500, "保存失败")
		return
	}
	reply(w, 200, map[string]any{"id": q.ID, "version": version + 1})
}

type paymentNode struct {
	Title    string `json:"title"`
	Day      string `json:"day"`
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
	State    string `json:"state"`
}
type managedProject struct {
	ID         int64         `json:"id"`
	Name       string        `json:"name"`
	Owner      *int64        `json:"owner_id"`
	OwnerName  string        `json:"owner_name"`
	Status     string        `json:"status"`
	Start      string        `json:"start_day"`
	Acceptance string        `json:"acceptance_day"`
	Payments   []paymentNode `json:"payments"`
	Version    int           `json:"version"`
}

const managedVisible = `not exists(select 1 from wm_project_candidate_states h where h.name=p.name and h.hidden) and not exists(select 1 from projects gp join wm_hidden_projects h on h.project_id=gp.id where gp.name=p.name) and (p.owner_id is null or exists(select 1 from employees e where e.id=p.owner_id and ` + humanVisible + `))`

func (s *Service) managedProjects(w http.ResponseWriter, r *http.Request) {
	rows, e := s.Store.Pool.Query(r.Context(), `select p.id,p.name,p.owner_id,coalesce(e.name,''),p.status,coalesce(p.start_day::text,''),coalesce(p.acceptance_day::text,''),p.payments,p.version from wm_managed_projects p left join employees e on e.id=p.owner_id where ($1 or p.owner_id=$2) and `+managedVisible+` order by p.updated_at desc,p.id`, who(r).employee.IsSuperAdmin, who(r).employee.ID)
	if e != nil {
		fail(w, 500, "读取项目管理失败")
		return
	}
	defer rows.Close()
	out := []managedProject{}
	for rows.Next() {
		var p managedProject
		var raw []byte
		if rows.Scan(&p.ID, &p.Name, &p.Owner, &p.OwnerName, &p.Status, &p.Start, &p.Acceptance, &raw, &p.Version) != nil || json.Unmarshal(raw, &p.Payments) != nil {
			fail(w, 500, "读取项目失败")
			return
		}
		out = append(out, p)
	}
	if rows.Err() != nil {
		fail(w, 500, "读取项目失败")
		return
	}
	reply(w, 200, map[string]any{"projects": out})
}
func (s *Service) saveManagedProject(w http.ResponseWriter, r *http.Request) {
	if !s.managementWriter(w, r) {
		return
	}
	var q managedProject
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 100000)).Decode(&q) != nil || strings.TrimSpace(q.Name) == "" || len(q.Name) > 300 || q.ID < 0 || !validDay(q.Start) || !validDay(q.Acceptance) || (q.Status != "active" && q.Status != "paused" && q.Status != "closed") || (q.Start != "" && q.Acceptance != "" && q.Acceptance < q.Start) {
		fail(w, 400, "检查项目名称、状态及日期顺序")
		return
	}
	if q.Payments == nil {
		q.Payments = []paymentNode{}
	}
	for _, p := range q.Payments {
		if strings.TrimSpace(p.Title) == "" || !validDay(p.Day) || (p.Amount != "" && !moneyPattern.MatchString(p.Amount)) || (p.Currency != "CNY" && p.Currency != "USD" && p.Currency != "EUR" && p.Currency != "HKD") || (p.State != "planned" && p.State != "received") {
			fail(w, 400, "检查回款节点的名称、日期、金额和状态")
			return
		}
	}
	tx, e := s.Store.Pool.Begin(r.Context())
	if e != nil {
		fail(w, 500, "保存失败")
		return
	}
	defer tx.Rollback(r.Context())
	u := who(r).employee
	if q.ID == 0 {
		if !u.IsSuperAdmin {
			fail(w, 403, "由管理员建立项目并指定负责人")
			return
		}
		if q.Version != 0 {
			fail(w, 409, "新项目版本应为零")
			return
		}
	} else {
		var owner *int64
		var ver int
		var name string
		e = tx.QueryRow(r.Context(), `select p.owner_id,p.version,p.name from wm_managed_projects p where p.id=$1 and `+managedVisible+` for update`, q.ID).Scan(&owner, &ver, &name)
		if e != nil {
			fail(w, 404, "项目不可见")
			return
		}
		if !u.IsSuperAdmin && (owner == nil || *owner != u.ID) {
			fail(w, 403, "只有项目负责人可以维护")
			return
		}
		if ver != q.Version {
			fail(w, 409, "项目已更新，请刷新")
			return
		}
		if !u.IsSuperAdmin {
			q.Owner = owner
		}
		q.Name = name
	}
	if q.Owner != nil {
		var ok bool
		e = tx.QueryRow(r.Context(), `select exists(select 1 from employees e where e.id=$1 and `+humanVisible+`)`, *q.Owner).Scan(&ok)
		if e != nil || !ok {
			fail(w, 400, "负责人必须是可见飞书成员")
			return
		}
	}
	raw, _ := json.Marshal(q.Payments)
	if q.ID == 0 {
		e = tx.QueryRow(r.Context(), `insert into wm_managed_projects(name,owner_id,status,start_day,acceptance_day,payments,updated_by) values($1,$2,$3,$4::date,$5::date,$6,$7) on conflict(name) do nothing returning id`, strings.TrimSpace(q.Name), q.Owner, q.Status, nullable(q.Start), nullable(q.Acceptance), raw, u.ID).Scan(&q.ID)
	} else {
		_, e = tx.Exec(r.Context(), `update wm_managed_projects set owner_id=$2,status=$3,start_day=$4::date,acceptance_day=$5::date,payments=$6,version=version+1,updated_by=$7,updated_at=now() where id=$1`, q.ID, q.Owner, q.Status, nullable(q.Start), nullable(q.Acceptance), raw, u.ID)
	}
	if e != nil {
		fail(w, 409, "项目名称已存在或保存冲突")
		return
	}
	_, e = tx.Exec(r.Context(), `insert into wm_managed_project_history(project_id,snapshot,editor_id)select id,to_jsonb(p),$2 from wm_managed_projects p where id=$1`, q.ID, u.ID)
	if e != nil || tx.Commit(r.Context()) != nil {
		fail(w, 500, "保存项目失败")
		return
	}
	reply(w, 200, map[string]any{"id": q.ID})
}

func (s *Service) expenseHistory(w http.ResponseWriter, r *http.Request) {
	id, e := idParam(r)
	if e != nil {
		fail(w, 400, "费用编号无效")
		return
	}
	rows, e := s.Store.Pool.Query(r.Context(), `select version,created_at,to_jsonb(v)-'editor_id' from wm_expense_versions v where expense_id=$1 and `+expenseVisible+` order by version desc`, id)
	if e != nil {
		fail(w, 500, "读取版本失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var version int
		var at time.Time
		var raw json.RawMessage
		if rows.Scan(&version, &at, &raw) != nil {
			fail(w, 500, "读取失败")
			return
		}
		out = append(out, map[string]any{"version": version, "created_at": at, "snapshot": raw})
	}
	if rows.Err() != nil {
		fail(w, 500, "读取失败")
		return
	}
	reply(w, 200, map[string]any{"versions": out})
}
func (s *Service) managedProjectHistory(w http.ResponseWriter, r *http.Request) {
	id, e := idParam(r)
	if e != nil {
		fail(w, 400, "项目编号无效")
		return
	}
	rows, e := s.Store.Pool.Query(r.Context(), `select (h.snapshot->>'version')::int,h.created_at,h.snapshot-'updated_by' from wm_managed_project_history h join wm_managed_projects p on p.id=h.project_id where p.id=$1 and ($2 or p.owner_id=$3) and `+managedVisible+` order by h.id desc`, id, who(r).employee.IsSuperAdmin, who(r).employee.ID)
	if e != nil {
		fail(w, 500, "读取版本失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var version int
		var at time.Time
		var raw json.RawMessage
		if rows.Scan(&version, &at, &raw) != nil {
			fail(w, 500, "读取失败")
			return
		}
		out = append(out, map[string]any{"version": version, "created_at": at, "snapshot": raw})
	}
	if rows.Err() != nil {
		fail(w, 500, "读取失败")
		return
	}
	reply(w, 200, map[string]any{"versions": out})
}

func (s *Service) managementWriter(w http.ResponseWriter, r *http.Request) bool {
	var ok bool
	e := s.Store.Pool.QueryRow(r.Context(), `select exists(select 1 from employees e where e.id=$1 and `+humanVisible+`)`, who(r).employee.ID).Scan(&ok)
	if e != nil || !ok {
		fail(w, 403, "私密或非人员账号不能维护共享管理记录")
		return false
	}
	return true
}
