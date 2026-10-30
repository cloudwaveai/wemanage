package web_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"aiseat/internal/capture"
	"aiseat/internal/secure"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/web"
	"aiseat/internal/workdigest"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestExtractionFromLogReviewAndPrivacy(t *testing.T) {
	ctx := context.Background()
	st := store.New(testutil.NewDB(t))
	set := settings.NewService(st)
	box, _ := secure.NewBox(make([]byte, 32))
	_ = st.EnsureSuperAdmin(ctx, "ou_extract_admin", "管理员")
	boss, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_extract_private", Name: "林伊默"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_extract_member", Name: "成员"})
	if err != nil {
		t.Fatal(err)
	}
	group, _ := st.CreateIPGroup(ctx, box, "extract", "", 10)
	acc, err := st.CreateAccount(ctx, store.Account{CPAAuthName: "extract.json", IPGroupID: group.ID, Provider: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	cfg := set.Get(ctx)
	cfg.WorkDigestAccountID = acc.ID
	if err := set.Put(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var payload struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if len(payload.Messages) != 2 || !strings.Contains(payload.Messages[1].Content, "可验证的源文字") {
			t.Errorf("model did not receive source")
		}
		answer := `{"cleaned_log":"整理草稿","limitations":"需要人工核对","knowledge":[{"title":"知识","content":"内容","evidence_quote":"可验证的源文字"}],"skills":[{"title":"Skill","content":"步骤","evidence_quote":"不存在的原文"}]}`
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": answer}}}, "usage": map[string]int{"prompt_tokens": 123, "completion_tokens": 45}})
	}))
	defer mock.Close()
	runner := &workdigest.Runner{Store: st, Settings: set, CPABaseURL: mock.URL, CPAAPIKey: "test", HC: mock.Client()}
	srv := web.NewServer(web.Deps{Store: st, Settings: set, Box: box, PublicBaseURL: "http://localhost", DevLogin: true, Digest: runner})
	r := chi.NewRouter()
	srv.Mount(r)
	ts := httptest.NewServer(r)
	defer ts.Close()
	admin := login(t, ts, "ou_extract_admin")
	ordinary := login(t, ts, "ou_extract_member")
	add := func(employee int64, missing bool) [16]byte {
		segs, _, err := capture.SplitRequest("chat", []byte(`{"messages":[{"role":"user","content":"可验证的源文字"}]}`), nil)
		if err != nil {
			t.Fatal(err)
		}
		var hashes [][32]byte
		var blobs []store.Blob
		for _, seg := range segs {
			hashes = append(hashes, seg.Hash)
			blobs = append(blobs, store.Blob{Hash: seg.Hash, Kind: seg.Kind, Data: seg.Data})
		}
		if !missing {
			if err := st.PutBlobs(ctx, blobs, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
		id := uuid.New()
		enc := capture.Encode(hashes, nil)
		if err := st.InsertRequest(ctx, store.RequestRow{ID: id, StartedAt: time.Now(), EmployeeID: employee, ClientKind: "codex", Endpoint: "chat", StatusCode: 200, ReqHashes: enc.Hashes}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	public := add(member.ID, false)
	private := add(boss.ID, false)
	payload := func(id [16]byte) string { return fmt.Sprintf(`{"request_id":"%s"}`, hex.EncodeToString(id[:])) }
	if resp, _ := ordinary.do("POST", "/api/admin/extraction/run", payload(public)); resp.StatusCode != 403 {
		t.Fatalf("nonadmin %d", resp.StatusCode)
	}
	if resp, _ := admin.do("POST", "/api/admin/extraction/run", payload(private)); resp.StatusCode != 404 || calls.Load() != 0 {
		t.Fatalf("private %d calls %d", resp.StatusCode, calls.Load())
	}
	resp, body := admin.do("POST", "/api/admin/extraction/run", payload(public))
	if resp.StatusCode != 200 || calls.Load() != 1 {
		t.Fatalf("run %d %s", resp.StatusCode, body)
	}
	resp, body = admin.do("GET", "/api/admin/extraction/runs", "")
	if resp.StatusCode != 200 {
		t.Fatalf("list %d %s", resp.StatusCode, body)
	}
	var list struct {
		Runs []struct {
			Items []struct {
				ID       int64  `json:"id"`
				Verified bool   `json:"evidence_verified"`
				Status   string `json:"status"`
			} `json:"items"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Runs) != 1 || len(list.Runs[0].Items) != 2 || !list.Runs[0].Items[0].Verified || list.Runs[0].Items[1].Verified {
		t.Fatalf("evidence verification: %s", body)
	}
	var inTokens, outTokens int64
	if err := st.Pool.QueryRow(ctx, `SELECT input_tokens,output_tokens FROM wm_extraction_runs LIMIT 1`).Scan(&inTokens, &outTokens); err != nil || inTokens != 123 || outTokens != 45 {
		t.Fatalf("model usage %d %d %v", inTokens, outTokens, err)
	}
	item := list.Runs[0].Items[0].ID
	resp, body = admin.do("POST", fmt.Sprintf("/api/admin/extraction/items/%d/review", item), `{"status":"confirmed","title":"修正知识","content":"人工确认的内容"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("review %d %s", resp.StatusCode, body)
	}
	var original, reviewed string
	if err := st.Pool.QueryRow(ctx, `SELECT title,reviewed_title FROM wm_extracted_items WHERE id=$1`, item).Scan(&original, &reviewed); err != nil || original != "知识" || reviewed != "修正知识" {
		t.Fatalf("original/review: %q %q %v", original, reviewed, err)
	}
	missingID := uuid.New()
	if err := st.InsertRequest(ctx, store.RequestRow{ID: missingID, StartedAt: time.Now(), EmployeeID: member.ID, ClientKind: "codex", Endpoint: "chat", StatusCode: 200, ReqHashes: make([]byte, 32)}); err != nil {
		t.Fatal(err)
	}
	resp, body = admin.do("POST", "/api/admin/extraction/run", payload(missingID))
	if resp.StatusCode != 409 || calls.Load() != 1 {
		t.Fatalf("missing source %d %s calls=%d", resp.StatusCode, body, calls.Load())
	}
	if err := st.SetSuperAdmin(ctx, boss.ID, true); err != nil {
		t.Fatal(err)
	}
	bossViewer := login(t, ts, "ou_extract_private")
	resp, body = bossViewer.do("POST", fmt.Sprintf("/api/admin/extraction/items/%d/review", list.Runs[0].Items[1].ID), `{"status":"confirmed","title":"私密审核标题","content":"私密审核内容"}`)
	if resp.StatusCode != 200 {
		t.Fatalf("private review %d %s", resp.StatusCode, body)
	}
	resp, body = admin.do("GET", "/api/admin/extraction/runs", "")
	if resp.StatusCode != 200 || strings.Contains(string(body), "私密审核内容") || strings.Contains(string(body), "私密审核标题") {
		t.Fatalf("private review leaked: %d %s", resp.StatusCode, body)
	}
	// Changing the subject's privacy hides all derivatives, including reviewed items.
	if _, err := st.Pool.Exec(ctx, `UPDATE employees SET private_work_since=now() WHERE id=$1`, member.ID); err != nil {
		t.Fatal(err)
	}
	resp, body = admin.do("GET", "/api/admin/extraction/runs", "")
	if resp.StatusCode != 200 || strings.Contains(string(body), "整理草稿") {
		t.Fatalf("private derivative leaked %d %s", resp.StatusCode, body)
	}
}
