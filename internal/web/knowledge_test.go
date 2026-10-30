package web_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aiseat/internal/knowledge"
	"aiseat/internal/secure"
	"aiseat/internal/settings"
	"aiseat/internal/store"
	"aiseat/internal/testutil"
	"aiseat/internal/web"
	"aiseat/internal/workdigest"
	"github.com/go-chi/chi/v5"
)

type knowledgeFake struct{}

func (knowledgeFake) Extract(_ context.Context, _, _ string) (string, string, workdigest.ModelUsage, error) {
	return `{"items":[{"category":"problem","title":"常见问题","content":"处理思路","project_hint":"","evidence":[{"source":1,"quote":"验证成功"}]}]}`, "fake", workdigest.ModelUsage{}, nil
}
func TestKnowledgeAdminReviewAndPrivacy(t *testing.T) {
	st := store.New(testutil.NewDB(t))
	ctx := context.Background()
	set := settings.NewService(st)
	box, _ := secure.NewBox(make([]byte, 32))
	_ = st.EnsureSuperAdmin(ctx, "ou_knowledge_admin", "管理员")
	subject, err := st.UpsertEmployeeByOpenID(ctx, store.Employee{FeishuOpenID: "ou_knowledge_subject", Name: "成员"})
	if err != nil {
		t.Fatal(err)
	}
	runner := &knowledge.Runner{Store: st, Extractor: knowledgeFake{}, Loc: time.FixedZone("CST", 8*3600)}
	day := runner.Day(time.Now()).AddDate(0, 0, -1)
	_ = st.StartWorkDigestDay(ctx, day)
	_ = st.SaveWorkDigest(ctx, store.WorkDigest{Day: day, EmployeeID: subject.ID, SummaryMD: "项目检查后验证成功", Model: "digest"})
	_ = st.FinishWorkDigestDay(ctx, day, "完成", "digest", "done", "")
	srv := web.NewServer(web.Deps{Store: st, Settings: set, Box: box, PublicBaseURL: "http://localhost", DevLogin: true, Knowledge: runner})
	router := chi.NewRouter()
	srv.Mount(router)
	ts := httptest.NewServer(router)
	defer ts.Close()
	admin := login(t, ts, "ou_knowledge_admin")
	ordinary := login(t, ts, "ou_knowledge_subject")
	if resp, _ := ordinary.do("GET", "/api/admin/knowledge/runs", ""); resp.StatusCode != 403 {
		t.Fatalf("non-admin %d", resp.StatusCode)
	}
	resp, body := admin.do("GET", "/api/admin/knowledge/schedule", "")
	var plan struct {
		Enabled bool `json:"enabled"`
		Queue   []struct {
			Digests int `json:"digests"`
			Bytes   int `json:"bytes"`
		} `json:"queue_preview"`
	}
	if err := json.Unmarshal(body, &plan); err != nil || resp.StatusCode != 200 || plan.Enabled || len(plan.Queue) != 2 || plan.Queue[0].Digests != 1 || plan.Queue[0].Bytes != len("项目检查后验证成功") {
		t.Fatalf("queue preview %d %s %v", resp.StatusCode, body, err)
	}
	if resp, body := admin.do("POST", "/api/admin/knowledge/run", `{"kind":"daily"}`); resp.StatusCode != 200 {
		t.Fatalf("run %d %s", resp.StatusCode, body)
	}
	resp, body = admin.do("GET", "/api/admin/knowledge/runs", "")
	if resp.StatusCode != 200 || !strings.Contains(string(body), "验证成功") {
		t.Fatalf("list %d %s", resp.StatusCode, body)
	}
	var data struct {
		Runs []struct {
			Items []struct {
				ID       int64 `json:"id"`
				Verified bool  `json:"evidence_verified"`
			} `json:"items"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(body, &data); err != nil || len(data.Runs) != 1 || len(data.Runs[0].Items) != 1 || !data.Runs[0].Items[0].Verified {
		t.Fatalf("result %s %v", body, err)
	}
	item := data.Runs[0].Items[0].ID
	if resp, body := admin.do("POST", fmt.Sprintf("/api/admin/knowledge/items/%d/review", item), `{"status":"confirmed","title":"修正标题","content":"核对后的内容"}`); resp.StatusCode != 200 {
		t.Fatalf("review %d %s", resp.StatusCode, body)
	}
	var raw, review string
	if err := st.Pool.QueryRow(ctx, `SELECT title,reviewed_title FROM wm_knowledge_items WHERE id=$1`, item).Scan(&raw, &review); err != nil || raw != "常见问题" || review != "修正标题" {
		t.Fatalf("original/review %s %s %v", raw, review, err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE employees SET private_work_since=now() WHERE id=$1`, subject.ID); err != nil {
		t.Fatal(err)
	}
	resp, body = admin.do("GET", "/api/admin/knowledge/runs", "")
	if resp.StatusCode != 200 || strings.Contains(string(body), "验证成功") || strings.Contains(string(body), "核对后的内容") {
		t.Fatalf("private leak %d %s", resp.StatusCode, body)
	}
	resp, body = admin.do("GET", "/api/admin/knowledge/schedule", "")
	if err := json.Unmarshal(body, &plan); err != nil || resp.StatusCode != 200 || len(plan.Queue) != 2 || plan.Queue[0].Digests != 0 || plan.Queue[0].Bytes != 0 {
		t.Fatalf("private queue %d %s %v", resp.StatusCode, body, err)
	}
	if resp, _ := admin.do("PUT", "/api/admin/knowledge/schedule", `{"enabled":false}`); resp.StatusCode != 200 {
		t.Fatalf("pause %d", resp.StatusCode)
	}
	var enabled bool
	if err := st.Pool.QueryRow(ctx, `SELECT enabled FROM wm_knowledge_schedule WHERE id=1`).Scan(&enabled); err != nil || enabled {
		t.Fatalf("pause %v %v", enabled, err)
	}
}
