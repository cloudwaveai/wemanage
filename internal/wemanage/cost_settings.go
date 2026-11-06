package wemanage

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

const costSettingsSchema = `CREATE TABLE IF NOT EXISTS wm_cost_setting_versions(id BIGSERIAL PRIMARY KEY,month DATE NOT NULL,settings JSONB NOT NULL,editor_id BIGINT NOT NULL REFERENCES employees(id),created_at TIMESTAMPTZ NOT NULL DEFAULT now());`

type costAssumptions struct {
	Rates      map[string]float64 `json:"rates"`
	ProUSD     float64            `json:"pro_usd"`
	PlusUSD    float64            `json:"plus_usd"`
	AccountUSD map[string]float64 `json:"account_usd"`
}

// Snapshot reference rates, not a live FX feed or card-settlement rate.
func defaultCostAssumptions() costAssumptions {
	return costAssumptions{Rates: map[string]float64{"CNY": 1, "USD": 6.7168, "EUR": 7.5191, "HKD": 0.85712}, ProUSD: 200, PlusUSD: 20, AccountUSD: map[string]float64{}}
}
func (s *Service) readCostSettings(ctx context.Context) (map[string]any, error) {
	rows, err := s.Store.Pool.Query(ctx, `select distinct on(month) id,to_char(month,'YYYY-MM'),settings,created_at from wm_cost_setting_versions order by month,id desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	months := map[string]any{}
	for rows.Next() {
		var id int64
		var month string
		var raw json.RawMessage
		var at time.Time
		if err = rows.Scan(&id, &month, &raw, &at); err != nil {
			return nil, err
		}
		months[month] = map[string]any{"version": id, "settings": raw, "updated_at": at}
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"defaults": defaultCostAssumptions(), "months": months, "rate_date": "2026-11-06", "rate_source": "https://www.safe.gov.cn/AppStructured/hlw/RMBQuery.do", "price_source": "https://learn.chatgpt.com/docs/pricing"}, nil
}
func (s *Service) saveCostSettings(w http.ResponseWriter, r *http.Request) {
	if !s.managementWriter(w, r) {
		return
	}
	var q struct {
		Month    string          `json:"month"`
		Version  int64           `json:"version"`
		Settings costAssumptions `json:"settings"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&q) != nil {
		fail(w, 400, "费用设置格式无效")
		return
	}
	date, err := time.Parse("2006-01", q.Month)
	valid := func(v float64, max float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= max }
	if err != nil || date.Format("2006-01") != q.Month || q.Version < 0 || !valid(q.Settings.ProUSD, 1000000) || !valid(q.Settings.PlusUSD, 1000000) || q.Settings.Rates["CNY"] != 1 {
		fail(w, 400, "月份、单价或汇率无效")
		return
	}
	if len(q.Settings.Rates) != 4 {
		fail(w, 400, "请填写全部币种汇率")
		return
	}
	for _, c := range []string{"USD", "EUR", "HKD"} {
		rate := q.Settings.Rates[c]
		if rate <= 0 || !valid(rate, 1000) {
			fail(w, 400, "汇率必须大于零且不超过 1000")
			return
		}
	}
	for id, price := range q.Settings.AccountUSD {
		if i, e := strconv.ParseInt(id, 10, 64); e != nil || i < 1 || !valid(price, 1000000) {
			fail(w, 400, "账号单价无效")
			return
		}
	}
	tx, err := s.Store.Pool.Begin(r.Context())
	if err != nil {
		fail(w, 500, "保存失败")
		return
	}
	defer tx.Rollback(r.Context())
	if _, err = tx.Exec(r.Context(), `select pg_advisory_xact_lock(hashtext($1))`, "wm-costs:"+q.Month); err != nil {
		fail(w, 500, "保存失败")
		return
	}
	var latest int64
	err = tx.QueryRow(r.Context(), `select id from wm_cost_setting_versions where month=$1::date order by id desc limit 1`, q.Month+"-01").Scan(&latest)
	if err != nil && err != pgx.ErrNoRows {
		fail(w, 500, "读取设置失败")
		return
	}
	if latest != q.Version {
		fail(w, 409, "设置已被更新，请刷新后重试")
		return
	}
	raw, _ := json.Marshal(q.Settings)
	if err = tx.QueryRow(r.Context(), `insert into wm_cost_setting_versions(month,settings,editor_id)values($1::date,$2,$3)returning id`, q.Month+"-01", raw, who(r).employee.ID).Scan(&latest); err != nil || tx.Commit(r.Context()) != nil {
		fail(w, 500, "保存设置失败")
		return
	}
	reply(w, 200, map[string]any{"version": latest})
}
