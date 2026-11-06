package wemanage

import (
	"encoding/json"
	"net/http"
	"time"
)

// costSources reads subscription metadata only. It never returns upstream credentials.
func (s *Service) costSources(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Pool.Query(r.Context(), `select a.id,a.provider,a.plan_type,a.status,coalesce(sub.plan,''),sub.active,sub.renews_at,sub.expires_at,sub.checked_at,coalesce(sub.error<>'',false),coalesce((select json_agg(json_build_object('id',e.id,'name',e.name) order by e.id) from account_members m join employees e on e.id=m.employee_id where m.account_id=a.id and `+humanVisible+`),'[]') from upstream_accounts a left join account_subscriptions sub on sub.account_id=a.id order by a.id`)
	if err != nil {
		fail(w, 500, "读取订阅来源失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var provider, plan, status, observed string
		var active *bool
		var renews, expires, checked *time.Time
		var failed bool
		var members json.RawMessage
		if rows.Scan(&id, &provider, &plan, &status, &observed, &active, &renews, &expires, &checked, &failed, &members) != nil {
			fail(w, 500, "读取订阅来源失败")
			return
		}
		out = append(out, map[string]any{"id": id, "provider": provider, "plan": plan, "account_status": status, "observed_plan": observed, "active": active, "renews_at": renews, "expires_at": expires, "checked_at": checked, "check_failed": failed, "members": members})
	}
	if rows.Err() != nil {
		fail(w, 500, "读取订阅来源失败")
		return
	}
	var channels int
	if s.Store.Pool.QueryRow(r.Context(), `select count(*) from api_channels`).Scan(&channels) != nil {
		fail(w, 500, "读取渠道状态失败")
		return
	}
	settings, err := s.readCostSettings(r.Context())
	if err != nil {
		fail(w, 500, "读取人民币成本设置失败")
		return
	}
	reply(w, 200, map[string]any{"subscriptions": out, "registered_api_channels": channels, "cost_settings": settings})
}
