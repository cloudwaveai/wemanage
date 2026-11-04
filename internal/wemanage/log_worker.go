package wemanage

import (
	"encoding/json"
	"net/http"
)

func (s *Service) logWorkerStatus(w http.ResponseWriter, r *http.Request) {
	var enabled bool
	var lastError string
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT enabled,last_error FROM wm_log_worker_control WHERE id=1`).Scan(&enabled, &lastError); err != nil {
		fail(w, 500, "读取整理状态失败")
		return
	}
	reply(w, 200, map[string]any{"enabled": enabled, "error": lastError})
}

func (s *Service) setLogWorkerStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || body.Enabled == nil {
		fail(w, 400, "状态无效")
		return
	}
	if *body.Enabled {
		var active bool
		if err := s.Store.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM wm_compact_jobs j WHERE j.phase='extract' AND j.state='queued')`).Scan(&active); err != nil {
			fail(w, 500, "读取整理状态失败")
			return
		}
		if active {
			fail(w, 409, "项目式整理正在处理，旧整理保持暂停")
			return
		}
	}
	var oldEnabled bool
	if err := s.Store.Pool.QueryRow(r.Context(), `SELECT enabled FROM wm_full_journal_settings WHERE id=1`).Scan(&oldEnabled); err != nil || oldEnabled {
		fail(w, 409, "旧整理程序仍启用，请先暂停")
		return
	}
	if _, err := s.Store.Pool.Exec(r.Context(), `UPDATE wm_log_worker_control SET enabled=$1,last_error='',updated_at=now() WHERE id=1`, *body.Enabled); err != nil {
		fail(w, 500, "保存状态失败")
		return
	}
	reply(w, 200, map[string]any{"enabled": *body.Enabled})
}
