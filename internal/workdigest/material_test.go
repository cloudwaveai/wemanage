package workdigest

import (
	"strings"
	"testing"
	"time"

	"aiseat/internal/store"
)

func turn(sid int64, at time.Time, role, text string) store.DayText {
	return store.DayText{SessionID: &sid, StartedAt: at, Role: role, Text: text}
}

func TestBuildMaterial(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	at := time.Date(2026, 10, 13, 9, 30, 0, 0, loc)
	m := BuildMaterial([]store.DayText{
		turn(1, at, "user", "<environment_context><cwd>/x</cwd></environment_context>"),
		turn(1, at, "user", "You are Codex, an agent based on GPT-6."),
		turn(1, at, "user", "帮我把登录页的报错\n改成中文"),
		turn(1, at.Add(time.Minute), "assistant", "好的，先看一下 login.tsx"),
		turn(1, at.Add(2*time.Minute), "user", "帮我把登录页的报错\n改成中文"), // repeated by a sub-agent
		turn(1, at.Add(3*time.Minute), "assistant", "已改好，测试通过。"),
		turn(2, at.Add(time.Hour), "user", "# AGENTS.md instructions for /x"),
		turn(2, at.Add(time.Hour), "assistant", "only context in this session"),
		turn(3, at.Add(2*time.Hour), "user", "整理一下周会纪要"),
	}, loc)

	if m.Sessions != 2 || m.Messages != 2 {
		t.Fatalf("sessions %d messages %d, want 2 and 2:\n%s", m.Sessions, m.Messages, m.Text)
	}
	for _, want := range []string{"## 会话 1（09:30–09:33，1 条消息）", "- 帮我把登录页的报错 改成中文",
		"助手最后的回复（开头）：已改好，测试通过。", "## 会话 2（11:30–11:30，1 条消息）", "- 整理一下周会纪要"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("missing %q in:\n%s", want, m.Text)
		}
	}
	for _, noise := range []string{"environment_context", "You are Codex", "AGENTS.md", "only context"} {
		if strings.Contains(m.Text, noise) {
			t.Errorf("noise %q kept:\n%s", noise, m.Text)
		}
	}
}

func TestBuildMaterialOnlyNoise(t *testing.T) {
	at := time.Now()
	m := BuildMaterial([]store.DayText{turn(1, at, "user", "<app-context>x</app-context>"), turn(1, at, "assistant", "hi")}, nil)
	if m.Text != "" || m.Sessions != 0 {
		t.Fatalf("want empty material, got %+v", m)
	}
}

// A long session gets its share of the budget; the rest of it is counted, not
// listed, and later sessions still appear.
func TestBuildMaterialBudget(t *testing.T) {
	at := time.Now()
	var turns []store.DayText
	for i := range 200 {
		turns = append(turns, turn(1, at, "user", strings.Repeat("长", 300)+string(rune('a'+i%26))+strings.Repeat("x", i)))
	}
	turns = append(turns, turn(2, at.Add(time.Hour), "user", "第二个会话的任务"))
	m := BuildMaterial(turns, nil)
	if n := len([]rune(m.Text)); n > maxMaterialRunes+2000 {
		t.Fatalf("material is %d runes", n)
	}
	if !strings.Contains(m.Text, "条消息未列出）") || !strings.Contains(m.Text, "第二个会话的任务") {
		t.Fatalf("budget not shared:\n%.600s", m.Text)
	}
	if m.Messages != 201 {
		t.Fatalf("messages = %d, want 201", m.Messages)
	}
}
