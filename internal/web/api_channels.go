package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"aiseat/internal/proxy"
	"aiseat/internal/store"
	"github.com/go-chi/chi/v5"
)

func (s *Server) handleListAPIChannels(w http.ResponseWriter, r *http.Request) {
	channels, err := s.deps.Store.ListAPIChannels(r.Context())
	if err != nil {
		writeErr(w, 500, "query failed")
		return
	}
	writeJSON(w, 200, map[string]any{"channels": channels})
}
func (s *Server) handleCreateAPIChannel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name       string `json:"name"`
		Protocol   string `json:"protocol"`
		BaseURL    string `json:"base_url"`
		Credential string `json:"credential"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in) != nil {
		writeErr(w, 400, "bad body")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	in.BaseURL = strings.TrimRight(strings.TrimSpace(in.BaseURL), "/")
	in.Credential = strings.TrimSpace(in.Credential)
	if in.Name == "" || len([]rune(in.Name)) > 80 || (in.Protocol != "openai" && in.Protocol != "anthropic") || !proxy.ValidChannelURL(in.BaseURL) || in.Credential == "" || len(in.Credential) > 4096 {
		writeErr(w, 400, "请填写名称、协议、HTTPS 基础地址和上游密钥")
		return
	}
	if s.deps.Box == nil {
		writeErr(w, 503, "加密服务不可用")
		return
	}
	c, err := s.deps.Store.CreateAPIChannel(r.Context(), store.APIChannel{Name: in.Name, Protocol: in.Protocol, BaseURL: in.BaseURL, CredentialCipher: s.deps.Box.Seal([]byte(in.Credential))})
	if err != nil {
		writeErr(w, 500, "cannot create")
		return
	}
	s.audit(r, "api_channel_create", "api_channel", strconv.FormatInt(c.ID, 10), map[string]any{"name": c.Name, "protocol": c.Protocol})
	writeJSON(w, 200, c)
}
func (s *Server) handleSetAPIChannel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeErr(w, 400, "bad id")
		return
	}
	var in struct {
		Active     *bool   `json:"active"`
		Credential *string `json:"credential"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&in) != nil || (in.Active == nil && in.Credential == nil) || (in.Active != nil && in.Credential != nil) {
		writeErr(w, 400, "请选择一项操作")
		return
	}
	var ok bool
	if in.Credential != nil {
		credential := strings.TrimSpace(*in.Credential)
		if credential == "" || len(credential) > 4096 || s.deps.Box == nil {
			writeErr(w, 400, "请填写有效的上游密钥")
			return
		}
		ok, err = s.deps.Store.RotateAPIChannelCredential(r.Context(), id, s.deps.Box.Seal([]byte(credential)))
	} else {
		ok, err = s.deps.Store.SetAPIChannelActive(r.Context(), id, *in.Active)
	}
	if err != nil {
		writeErr(w, 500, "cannot update")
		return
	}
	if !ok {
		writeErr(w, 404, "not found")
		return
	}
	action := map[string]any{"credential_rotated": in.Credential != nil}
	if in.Active != nil {
		action["active"] = *in.Active
	}
	s.audit(r, "api_channel_update", "api_channel", strconv.FormatInt(id, 10), action)
	writeJSON(w, 200, map[string]any{"ok": true})
}
