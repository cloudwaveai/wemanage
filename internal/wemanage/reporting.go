package wemanage

import (
	"net/http"
	"time"
)

// usageReport reads recorded usage only. Private people are excluded even if
// the viewer is their owner: this endpoint is for shared reports.
func (s *Service) usageReport(w http.ResponseWriter, r *http.Request) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		fail(w, 500, "时区不可用")
		return
	}
	month := r.URL.Query().Get("month")
	if month == "" {
		month = time.Now().In(loc).Format("2006-01")
	}
	from, err := time.ParseInLocation("2006-01", month, loc)
	if err != nil || from.Format("2006-01") != month {
		fail(w, 400, "月份格式应为 YYYY-MM")
		return
	}
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT date_trunc('hour', r.started_at AT TIME ZONE 'Asia/Shanghai'), e.id,e.name,COALESCE(p.id,0),COALESCE(p.name,'未归属项目'),COALESCE(p.category,''),COALESCE(NULLIF(r.model_requested,''),'未记录'),count(*),COALESCE(sum(r.tokens_input),0),COALESCE(sum(r.tokens_output),0),COALESCE(rate.currency,''),count(*) FILTER (WHERE rate.origin IS NOT NULL),sum(((GREATEST(r.tokens_input-r.tokens_cached,0)*rate.input_per_million)+(r.tokens_cached*rate.cached_per_million)+(r.tokens_output*rate.output_per_million))/1000000)::float8 FROM requests r JOIN employees e ON e.id=r.employee_id LEFT JOIN api_keys k ON k.id=r.api_key_id LEFT JOIN request_projects rp ON rp.request_id=r.id LEFT JOIN projects p ON p.id=COALESCE(rp.project_id,k.project_id) `+priceJoin+` WHERE r.started_at >= $1 AND r.started_at < $2 AND e.private_work_since IS NULL AND e.name <> '林伊默' AND NOT EXISTS (SELECT 1 FROM wm_hidden_projects h WHERE h.project_id=p.id) GROUP BY 1,2,3,4,5,6,7,11 ORDER BY 1,2,4,7,11`, from, from.AddDate(0, 1, 0))
	if err != nil {
		fail(w, 500, "读取用量失败")
		return
	}
	defer rows.Close()
	type entry struct {
		Hour           string   `json:"hour"`
		EmployeeID     int64    `json:"employee_id"`
		Employee       string   `json:"employee"`
		ProjectID      int64    `json:"project_id"`
		Project        string   `json:"project"`
		Category       string   `json:"category"`
		Model          string   `json:"model"`
		Requests       int64    `json:"requests"`
		Input          int64    `json:"input_tokens"`
		Output         int64    `json:"output_tokens"`
		Currency       string   `json:"currency"`
		PricedRequests int64    `json:"priced_requests"`
		EstimatedCost  *float64 `json:"estimated_cost"`
	}
	result := []entry{}
	for rows.Next() {
		var x entry
		var hour time.Time
		if err = rows.Scan(&hour, &x.EmployeeID, &x.Employee, &x.ProjectID, &x.Project, &x.Category, &x.Model, &x.Requests, &x.Input, &x.Output, &x.Currency, &x.PricedRequests, &x.EstimatedCost); err != nil {
			fail(w, 500, "读取用量失败")
			return
		}
		x.Hour = hour.Format("2006-01-02 15:04")
		result = append(result, x)
	}
	if rows.Err() != nil {
		fail(w, 500, "读取用量失败")
		return
	}
	reply(w, 200, map[string]any{"month": month, "timezone": "Asia/Shanghai", "rows": result, "note": "仅含已记录调用；未归属项目单列，Token 不代表工作成果。"})
}
