package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"aiseat/internal/store"

	"github.com/go-chi/chi/v5"
)

// Administrators decide, per account, which models it may be used with: the
// account's models as CPA lists them, each allowed or not, with how much it
// was used lately.

type accountModel struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Created    int64  `json:"created"`
	Allowed    bool   `json:"allowed"`
	Offered    bool   `json:"offered"` // CPA lists it for the account
	Requests7d int64  `json:"requests_7d"`
}

func (s *Server) handleAccountModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	acc, err := s.deps.Store.GetAccount(ctx, accID)
	if err != nil {
		writeErr(w, http.StatusNotFound, "account not found")
		return
	}
	if s.deps.Models == nil {
		writeErr(w, http.StatusServiceUnavailable, "model list unavailable")
		return
	}
	models, err := s.deps.Models(ctx, acc)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "暂时查不到这个账号的模型列表，请稍后再试")
		return
	}
	usage, err := s.deps.Store.ModelUsage(ctx, acc.ID, time.Now().AddDate(0, 0, -7))
	if err != nil {
		s.deps.Log.Warn("account models: usage", "account", acc.ID, "err", err)
	}
	out := make([]accountModel, 0, len(models))
	seen := map[string]bool{}
	for _, m := range models {
		seen[strings.ToLower(m.ID)] = true
		out = append(out, accountModel{ID: m.ID, Name: m.Name, Created: m.Created, Allowed: acc.AllowsModel(m.ID),
			Offered: true, Requests7d: usage[m.ID]})
	}
	// An allowed model the account no longer offers stays visible, to be removed.
	for _, id := range acc.AllowedModels {
		if !seen[strings.ToLower(id)] {
			out = append(out, accountModel{ID: id, Allowed: true, Requests7d: usage[id]})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	writeJSON(w, http.StatusOK, map[string]any{"restricted": acc.AllowedModels != nil, "models": out})
}

func (s *Server) handleSetAccountModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accID, _ := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	var body struct {
		Restricted bool     `json:"restricted"`
		Models     []string `json:"models"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	if _, err := s.deps.Store.GetAccount(ctx, accID); err != nil {
		writeErr(w, http.StatusNotFound, "account not found")
		return
	}
	var models []string
	if body.Restricted {
		models = []string{}
		seen := map[string]bool{}
		for _, m := range body.Models {
			m = strings.TrimSpace(m)
			if m == "" || seen[m] {
				continue
			}
			if !modelIDPattern.MatchString(m) {
				writeErr(w, http.StatusBadRequest, fmt.Sprintf("模型名不对：%s", m))
				return
			}
			seen[m] = true
			models = append(models, m)
		}
		if len(models) == 0 {
			writeErr(w, http.StatusBadRequest, "至少要留一个可用的模型；不想限制的话，选「全部模型可用」")
			return
		}
	}
	if err := s.deps.Store.SetAccountModels(ctx, accID, models); err != nil {
		writeErr(w, http.StatusInternalServerError, "cannot update")
		return
	}
	s.audit(r, "account_models", "account", fmt.Sprint(accID), map[string]any{"restricted": body.Restricted, "models": models})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// employeeModels lists the models an employee may use across their accounts
// (each account's models as its policy allows), newest first.
func (s *Server) employeeModels(ctx context.Context, accs []store.Account) []store.ModelInfo {
	if s.deps.Models == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []store.ModelInfo
	for _, a := range accs {
		if a.Status != "active" {
			continue
		}
		ms, err := s.deps.Models(ctx, a)
		if err != nil {
			continue
		}
		for _, m := range ms {
			if !seen[m.ID] && a.AllowsModel(m.ID) {
				seen[m.ID] = true
				out = append(out, m)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out
}
