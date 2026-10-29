package store

import (
	"context"
	"time"
)

type Project struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Category  string    `json:"category"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) CreateProject(ctx context.Context, name, category string) (Project, error) {
	var p Project
	err := s.Pool.QueryRow(ctx, `INSERT INTO projects(name,category) VALUES($1,$2) RETURNING id,name,category,created_at`, name, category).Scan(&p.ID, &p.Name, &p.Category, &p.CreatedAt)
	return p, err
}
func (s *Store) Projects(ctx context.Context) ([]Project, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id,name,category,created_at FROM projects ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Category, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) AssignRequestProject(ctx context.Context, requestID [16]byte, projectID, actor int64) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `INSERT INTO request_projects(request_id,project_id,assigned_by) VALUES($1,$2,$3)
 ON CONFLICT(request_id) DO UPDATE SET project_id=EXCLUDED.project_id,assigned_by=EXCLUDED.assigned_by,assigned_at=now()
 WHERE request_projects.project_id IS DISTINCT FROM EXCLUDED.project_id`, requestID[:], projectID, actor)
	return tag.RowsAffected() == 1, err
}

type ChannelRate struct {
	ChannelID   int64     `json:"channel_id"`
	Model       string    `json:"model"`
	Currency    string    `json:"currency"`
	Input       float64   `json:"input_per_million"`
	Cached      float64   `json:"cached_per_million"`
	Output      float64   `json:"output_per_million"`
	EffectiveAt time.Time `json:"effective_at"`
}

func (s *Store) AddChannelRate(ctx context.Context, r ChannelRate) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO api_channel_rates(channel_id,model,currency,input_per_million,cached_per_million,output_per_million,effective_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, r.ChannelID, r.Model, r.Currency, r.Input, r.Cached, r.Output, r.EffectiveAt)
	return err
}
func (s *Store) ChannelRates(ctx context.Context, channelID int64) ([]ChannelRate, error) {
	rows, err := s.Pool.Query(ctx, `SELECT channel_id,model,currency,input_per_million::float8,cached_per_million::float8,output_per_million::float8,effective_at FROM api_channel_rates WHERE channel_id=$1 ORDER BY model,effective_at DESC`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChannelRate{}
	for rows.Next() {
		var r ChannelRate
		if err := rows.Scan(&r.ChannelID, &r.Model, &r.Currency, &r.Input, &r.Cached, &r.Output, &r.EffectiveAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type ProjectUsage struct {
	EmployeeID     int64   `json:"employee_id"`
	EmployeeName   string  `json:"employee_name"`
	ProjectID      *int64  `json:"project_id"`
	ProjectName    string  `json:"project_name"`
	Category       string  `json:"category"`
	Model          string  `json:"model"`
	Upstream       string  `json:"upstream"`
	Currency       string  `json:"currency"`
	Requests       int64   `json:"requests"`
	Input          int64   `json:"input"`
	Cached         int64   `json:"cached"`
	Output         int64   `json:"output"`
	PricedRequests int64   `json:"priced_requests"`
	EstimatedCost  float64 `json:"estimated_cost"`
}

func (s *Store) ProjectUsage(ctx context.Context, from, to time.Time) ([]ProjectUsage, error) {
	rows, err := s.Pool.Query(ctx, `
 SELECT r.employee_id,COALESCE(e.name,''),p.id,COALESCE(p.name,''),COALESCE(p.category,''),r.model_requested,COALESCE(c.name,a.provider,'未记录'),
 COALESCE(rate.currency,''),COUNT(*),COALESCE(SUM(r.tokens_input),0),COALESCE(SUM(r.tokens_cached),0),COALESCE(SUM(r.tokens_output),0),COUNT(rate.id),
 COALESCE(SUM(((GREATEST(r.tokens_input-r.tokens_cached,0)*rate.input_per_million)+(r.tokens_cached*rate.cached_per_million)+(r.tokens_output*rate.output_per_million))/1000000),0)::float8
 FROM requests r JOIN employees e ON e.id=r.employee_id LEFT JOIN api_keys k ON k.id=r.api_key_id
 LEFT JOIN api_channels c ON c.id=k.api_channel_id LEFT JOIN upstream_accounts a ON a.id=r.account_id
 LEFT JOIN request_projects rp ON rp.request_id=r.id LEFT JOIN projects p ON p.id=COALESCE(rp.project_id,k.project_id)
 LEFT JOIN LATERAL (SELECT id,currency,input_per_million,cached_per_million,output_per_million FROM api_channel_rates pr WHERE pr.channel_id=k.api_channel_id AND pr.model=r.model_requested AND pr.effective_at<=r.started_at ORDER BY pr.effective_at DESC LIMIT 1) rate ON true
 WHERE r.started_at >= $1 AND r.started_at < $2 AND e.private_work_since IS NULL AND e.name <> '林伊默'
 GROUP BY r.employee_id,e.name,p.id,p.name,p.category,r.model_requested,c.name,a.provider,rate.currency
 ORDER BY e.name,p.name,r.model_requested,rate.currency`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjectUsage{}
	for rows.Next() {
		var u ProjectUsage
		if err := rows.Scan(&u.EmployeeID, &u.EmployeeName, &u.ProjectID, &u.ProjectName, &u.Category, &u.Model, &u.Upstream, &u.Currency, &u.Requests, &u.Input, &u.Cached, &u.Output, &u.PricedRequests, &u.EstimatedCost); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

type ProjectEvent struct {
	ID           int64     `json:"id"`
	ProjectID    int64     `json:"project_id"`
	EmployeeID   int64     `json:"employee_id"`
	EmployeeName string    `json:"employee_name"`
	OccurredAt   time.Time `json:"occurred_at"`
	Content      string    `json:"content"`
	Evidence     string    `json:"evidence"`
	CreatedAt    time.Time `json:"created_at"`
}

func (s *Store) CreateProjectEvent(ctx context.Context, event ProjectEvent, actor int64) (ProjectEvent, error) {
	err := s.Pool.QueryRow(ctx, `INSERT INTO project_events(project_id,employee_id,occurred_at,content,evidence,created_by) VALUES($1,$2,$3,$4,$5,$6) RETURNING id,created_at`, event.ProjectID, event.EmployeeID, event.OccurredAt, event.Content, event.Evidence, actor).Scan(&event.ID, &event.CreatedAt)
	return event, err
}
func (s *Store) ProjectEvents(ctx context.Context, projectID, viewerID int64) ([]ProjectEvent, error) {
	rows, err := s.Pool.Query(ctx, `SELECT pe.id,pe.project_id,pe.employee_id,e.name,pe.occurred_at,pe.content,pe.evidence,pe.created_at FROM project_events pe JOIN employees e ON e.id=pe.employee_id WHERE pe.project_id=$1 AND (pe.employee_id=$2 OR (e.private_work_since IS NULL AND e.name <> '林伊默')) ORDER BY pe.occurred_at DESC,pe.id DESC`, projectID, viewerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjectEvent{}
	for rows.Next() {
		var e ProjectEvent
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.EmployeeID, &e.EmployeeName, &e.OccurredAt, &e.Content, &e.Evidence, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) RequestProject(ctx context.Context, requestID [16]byte) (projectID *int64, manual bool, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT COALESCE(rp.project_id,k.project_id),rp.project_id IS NOT NULL FROM requests r LEFT JOIN request_projects rp ON rp.request_id=r.id LEFT JOIN api_keys k ON k.id=r.api_key_id WHERE r.id=$1`, requestID[:]).Scan(&projectID, &manual)
	return
}

type WorkReview struct {
	ID          int64     `json:"id"`
	Kind        string    `json:"kind"`
	PeriodStart time.Time `json:"period_start"`
	PeriodEnd   time.Time `json:"period_end"`
	Content     string    `json:"content"`
	CreatedBy   int64     `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Store) CreateWorkReview(ctx context.Context, r WorkReview) (WorkReview, error) {
	err := s.Pool.QueryRow(ctx, `INSERT INTO work_reviews(kind,period_start,period_end,content,created_by) VALUES($1,$2,$3,$4,$5) RETURNING id,created_at`, r.Kind, r.PeriodStart, r.PeriodEnd, r.Content, r.CreatedBy).Scan(&r.ID, &r.CreatedAt)
	return r, err
}
func (s *Store) WorkReviews(ctx context.Context, viewer int64) ([]WorkReview, error) {
	rows, err := s.Pool.Query(ctx, `SELECT wr.id,wr.kind,wr.period_start,wr.period_end,wr.content,wr.created_by,wr.created_at FROM work_reviews wr JOIN employees e ON e.id=wr.created_by WHERE wr.created_by=$1 OR (e.private_work_since IS NULL AND e.name <> '林伊默') ORDER BY wr.created_at DESC,wr.id DESC`, viewer)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkReview{}
	for rows.Next() {
		var r WorkReview
		if err := rows.Scan(&r.ID, &r.Kind, &r.PeriodStart, &r.PeriodEnd, &r.Content, &r.CreatedBy, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type CaptureCoverage struct {
	Client   string `json:"client"`
	Endpoint string `json:"endpoint"`
	Requests int64  `json:"requests"`
	WithText int64  `json:"with_text"`
	Purged   int64  `json:"purged"`
}

func (s *Store) CaptureCoverage(ctx context.Context, from, to time.Time) ([]CaptureCoverage, error) {
	rows, err := s.Pool.Query(ctx, `SELECT r.client_kind,r.endpoint,COUNT(*),COUNT(*) FILTER (WHERE EXISTS(SELECT 1 FROM request_texts t WHERE t.request_id=r.id)),COUNT(*) FILTER (WHERE r.bodies_purged) FROM requests r JOIN employees e ON e.id=r.employee_id WHERE r.started_at >= $1 AND r.started_at < $2 AND e.private_work_since IS NULL AND e.name <> '林伊默' GROUP BY r.client_kind,r.endpoint ORDER BY r.client_kind,r.endpoint`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CaptureCoverage{}
	for rows.Next() {
		var c CaptureCoverage
		if err := rows.Scan(&c.Client, &c.Endpoint, &c.Requests, &c.WithText, &c.Purged); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
