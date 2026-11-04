package wemanage

import (
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Channel-specific rates win, then the last manual event; standard events restore
// the current reference snapshot, which deliberately also applies to older usage.
const priceJoin = ` LEFT JOIN LATERAL (SELECT id,currency,input_per_million,cached_per_million,output_per_million FROM api_channel_rates pr WHERE pr.channel_id=k.api_channel_id AND pr.model=r.model_requested AND pr.effective_at<=r.started_at ORDER BY pr.effective_at DESC,pr.id DESC LIMIT 1) channel_price ON true
 LEFT JOIN LATERAL (SELECT kind,currency,input_per_million,cached_per_million,output_per_million FROM wm_model_price_overrides m WHERE m.model=r.model_requested AND m.effective_at<=r.started_at ORDER BY m.effective_at DESC,m.id DESC LIMIT 1) manual_price ON true
 LEFT JOIN wm_model_price_catalog reference_price ON reference_price.model=r.model_requested
 LEFT JOIN LATERAL (SELECT CASE WHEN channel_price.id IS NOT NULL THEN 'channel' WHEN manual_price.kind='manual' THEN 'manual' WHEN reference_price.model IS NOT NULL THEN 'reference' END AS origin,
 CASE WHEN channel_price.id IS NOT NULL THEN channel_price.currency WHEN manual_price.kind='manual' THEN manual_price.currency ELSE reference_price.currency END AS currency,
 CASE WHEN channel_price.id IS NOT NULL THEN channel_price.input_per_million WHEN manual_price.kind='manual' THEN manual_price.input_per_million ELSE reference_price.input_per_million END AS input_per_million,
 CASE WHEN channel_price.id IS NOT NULL THEN channel_price.cached_per_million WHEN manual_price.kind='manual' THEN manual_price.cached_per_million ELSE reference_price.cached_per_million END AS cached_per_million,
 CASE WHEN channel_price.id IS NOT NULL THEN channel_price.output_per_million WHEN manual_price.kind='manual' THEN manual_price.output_per_million ELSE reference_price.output_per_million END AS output_per_million) rate ON true `

type priceEntry struct {
	Model       string    `json:"model"`
	Kind        string    `json:"kind"`
	Currency    string    `json:"currency"`
	Input       float64   `json:"input"`
	Cached      float64   `json:"cached"`
	Output      float64   `json:"output"`
	EffectiveAt time.Time `json:"effective_at"`
	CreatedAt   time.Time `json:"created_at"`
	Author      string    `json:"author"`
}

func (s *Service) modelPrices(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT model,currency,input_per_million::float8,cached_per_million::float8,output_per_million::float8,source,checked_on::text FROM wm_model_price_catalog ORDER BY model`)
	if err != nil {
		fail(w, 500, "读取参考价格失败")
		return
	}
	defer rows.Close()
	type reference struct {
		Model    string  `json:"model"`
		Currency string  `json:"currency"`
		Input    float64 `json:"input"`
		Cached   float64 `json:"cached"`
		Output   float64 `json:"output"`
		Source   string  `json:"source"`
		Checked  string  `json:"checked"`
	}
	refs := []reference{}
	for rows.Next() {
		var x reference
		if rows.Scan(&x.Model, &x.Currency, &x.Input, &x.Cached, &x.Output, &x.Source, &x.Checked) != nil {
			fail(w, 500, "读取参考价格失败")
			return
		}
		refs = append(refs, x)
	}
	if rows.Err() != nil {
		fail(w, 500, "读取参考价格失败")
		return
	}
	rows.Close()
	hist, err := s.Store.Pool.Query(r.Context(), `SELECT m.model,m.kind,m.currency,m.input_per_million::float8,m.cached_per_million::float8,m.output_per_million::float8,m.effective_at,m.created_at,e.name FROM wm_model_price_overrides m JOIN employees e ON e.id=m.created_by ORDER BY m.created_at DESC,m.id DESC LIMIT 500`)
	if err != nil {
		fail(w, 500, "读取价格历史失败")
		return
	}
	defer hist.Close()
	entries := []priceEntry{}
	for hist.Next() {
		var x priceEntry
		if hist.Scan(&x.Model, &x.Kind, &x.Currency, &x.Input, &x.Cached, &x.Output, &x.EffectiveAt, &x.CreatedAt, &x.Author) != nil {
			fail(w, 500, "读取价格历史失败")
			return
		}
		entries = append(entries, x)
	}
	if hist.Err() != nil {
		fail(w, 500, "读取价格历史失败")
		return
	}
	reply(w, 200, map[string]any{"reference": refs, "history": entries})
}

var modelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,149}$`)

func (s *Service) saveModelPrice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Model       string   `json:"model"`
		Kind        string   `json:"kind"`
		Currency    string   `json:"currency"`
		Input       *float64 `json:"input"`
		Cached      *float64 `json:"cached"`
		Output      *float64 `json:"output"`
		EffectiveAt string   `json:"effective_at"`
	}
	if !readBody(w, r, &in) {
		return
	}
	in.Model = strings.TrimSpace(in.Model)
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	effective, err := time.Parse(time.RFC3339, in.EffectiveAt)
	if err != nil || !modelName.MatchString(in.Model) || (in.Kind != "manual" && in.Kind != "standard") {
		fail(w, 400, "模型名称、操作或生效时间无效")
		return
	}
	input, cached, output := 0.0, 0.0, 0.0
	if in.Kind == "manual" {
		if in.Currency != "USD" && in.Currency != "CNY" || in.Input == nil || in.Cached == nil || in.Output == nil {
			fail(w, 400, "请填写币种和三项单价")
			return
		}
		input, cached, output = *in.Input, *in.Cached, *in.Output
		for _, v := range []float64{input, cached, output} {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1000000 {
				fail(w, 400, "单价必须在 0 到 1000000 之间")
				return
			}
		}
	} else {
		in.Currency = "USD"
	}
	_, err = s.Store.Pool.Exec(r.Context(), `INSERT INTO wm_model_price_overrides(model,kind,currency,input_per_million,cached_per_million,output_per_million,effective_at,created_by) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, in.Model, in.Kind, in.Currency, input, cached, output, effective, who(r).employee.ID)
	if err != nil {
		fail(w, 500, "保存价格失败")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
