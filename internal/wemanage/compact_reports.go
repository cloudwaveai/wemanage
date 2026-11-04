package wemanage

import "net/http"

// A report is suppressed if any of its sources has become private.
func (s *Service) compactReports(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Pool.Query(r.Context(), `select r.id,r.day::text,count(*) filter(where j.phase='extract'),count(*) filter(where j.phase='extract' and j.state='done'),count(*) filter(where j.state='running'),count(*) filter(where j.state='failed'),coalesce((select d.output from wm_compact_jobs d where d.run_id=r.id and d.phase='daily' and d.state='done' and not exists(select 1 from wm_compact_jobs later where later.run_id=r.id and later.phase='daily' and (later.level>d.level or (later.level=d.level and later.id<>d.id))) order by d.level desc limit 1),''),coalesce(sum(j.input_tokens),0),coalesce(sum(j.output_tokens),0) from wm_full_journal_runs r join wm_compact_jobs j on j.run_id=r.id where `+visibleKnowledge+` group by r.id,r.day order by r.day desc,r.id desc`)
	if err != nil {
		fail(w, 500, "读取项目式日报失败")
		return
	}
	defer rows.Close()
	type item struct {
		Run                          int64  `json:"run_id"`
		Day                          string `json:"day"`
		Total, Done, Running, Failed int64
		Report                       string `json:"report"`
		Input, Output                int64
	}
	out := []item{}
	for rows.Next() {
		var v item
		if rows.Scan(&v.Run, &v.Day, &v.Total, &v.Done, &v.Running, &v.Failed, &v.Report, &v.Input, &v.Output) != nil {
			fail(w, 500, "读取项目式日报失败")
			return
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		fail(w, 500, "读取项目式日报失败")
		return
	}
	reply(w, 200, map[string]any{"reports": out})
}
