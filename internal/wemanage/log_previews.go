package wemanage

import (
	"encoding/json"
	"net/http"
)

// Previews are explicitly partial and share the source journal's privacy.
func (s *Service) logPreviews(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.Pool.Query(r.Context(), `SELECT p.id,r.day::text,p.session_id,p.state,p.raw_output,p.model,p.input_tokens,p.output_tokens,p.error,to_jsonb(p.source_batch_ids) FROM wm_log_report_previews p JOIN wm_full_journal_runs r ON r.id=p.journal_run_id WHERE `+visibleKnowledge+` ORDER BY p.id DESC`)
	if err != nil {
		fail(w, 500, "读取日报预览失败")
		return
	}
	defer rows.Close()
	type preview struct {
		ID           int64           `json:"id"`
		Day          string          `json:"day"`
		SessionID    int64           `json:"session_id"`
		State        string          `json:"state"`
		Output       string          `json:"output"`
		Model        string          `json:"model"`
		Input        *int64          `json:"input_tokens"`
		OutputTokens *int64          `json:"output_tokens"`
		Error        string          `json:"error"`
		BatchIDs     json.RawMessage `json:"batch_ids"`
	}
	out := []preview{}
	for rows.Next() {
		var v preview
		if rows.Scan(&v.ID, &v.Day, &v.SessionID, &v.State, &v.Output, &v.Model, &v.Input, &v.OutputTokens, &v.Error, &v.BatchIDs) != nil {
			fail(w, 500, "读取日报预览失败")
			return
		}
		out = append(out, v)
	}
	if rows.Err() != nil {
		fail(w, 500, "读取日报预览失败")
		return
	}
	reply(w, 200, map[string]any{"previews": out})
}
