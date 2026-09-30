package report

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"aiseat/internal/store"
)

// System prompts. The LLM writes only prose; it never supplies numbers. The
// result is a draft of the employee's own weekly report, for them to copy and
// edit: it states what was done and never judges effort, output or hours.
const (
	sessionSystemPrompt = "用一两句中文概括这次对话里做了什么工作：处理的任务、完成的改动或得出的结论。只写事情本身，不做任何评价。"
	weeklySystemPrompt  = "下面是一个人本周工作内容的概要。请用中文替他起草本周工作周报，让他复制后稍作修改就能提交：第一人称，按事项分条列出完成了什么、进展到哪一步，同类事项合并，语言简洁、书面。只根据材料写，不要编造材料里没有的事情、数据或下周计划；不要提到 AI、Codex 或对话；不要评价工作量、效率或投入时间。"
)

// maxSessionsPerReport caps how many sessions are summarized per employee.
const maxSessionsPerReport = 20

// Sender pushes a report to an employee. *feishu.Client satisfies this.
type Sender interface {
	SendText(ctx context.Context, openID, text string) error
	SendCard(ctx context.Context, openID string, card any) error
}

// Deps are the collaborators for generating weekly reports.
type Deps struct {
	Store   *store.Store
	LLM     LLM
	Sender  Sender // may be nil to skip pushing
	Loc     *time.Location
	Enabled bool
}

// WeekStart returns 00:00 of the Monday of t's week in loc.
func WeekStart(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	t = t.In(loc)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	offset := (int(day.Weekday()) + 6) % 7 // Monday = 0
	return day.AddDate(0, 0, -offset)
}

// GenerateForWeek writes, stores and sends the recap for the week starting at
// weekStart (a Monday 00:00) to every active employee who used the gateway
// that week and has not turned the recap off. It is a no-op when disabled. A
// single employee's failure does not abort the batch; an error is returned
// only when every employee failed.
func GenerateForWeek(ctx context.Context, d Deps, weekStart time.Time) error {
	if !d.Enabled {
		return nil
	}
	from := weekStart
	to := from.AddDate(0, 0, 7)

	ids, err := d.Store.ReportRecipients(ctx, from, to)
	if err != nil {
		return err
	}

	var errs []error
	ok := 0
	for _, empID := range ids {
		if err := generateOne(ctx, d, empID, from, to); err != nil {
			errs = append(errs, fmt.Errorf("employee %d: %w", empID, err))
			continue
		}
		ok++
	}

	if ok == 0 && len(errs) > 0 {
		var b strings.Builder
		for i, e := range errs {
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString(e.Error())
		}
		return fmt.Errorf("report: all %d employee(s) failed: %s", len(errs), b.String())
	}
	return nil
}

// generateOne collects, summarizes, stores, and sends one employee's recap.
func generateOne(ctx context.Context, d Deps, employeeID int64, from, to time.Time) error {
	mat, err := Collect(ctx, d.Store, employeeID, from, to)
	if err != nil {
		return fmt.Errorf("collect: %w", err)
	}

	sessions := mat.Sessions
	if len(sessions) > maxSessionsPerReport {
		sessions = sessions[:maxSessionsPerReport]
	}

	var sessionSummaries []string
	for _, sess := range sessions {
		summary, err := d.LLM.Complete(ctx, sessionSystemPrompt, joinTurns(sess.Texts))
		if err != nil {
			return fmt.Errorf("summarize session %d: %w", sess.SessionID, err)
		}
		summary = strings.TrimSpace(summary)
		if summary != "" {
			sessionSummaries = append(sessionSummaries, summary)
		}
	}

	summaryMD, err := d.LLM.Complete(ctx, weeklySystemPrompt, strings.Join(sessionSummaries, "\n"))
	if err != nil {
		return fmt.Errorf("weekly summary: %w", err)
	}

	// Numbers are taken from SQL (mat.Stats), never from the model output.
	statsJSON, err := json.Marshal(mat.Stats)
	if err != nil {
		return fmt.Errorf("marshal stats: %w", err)
	}
	id, err := d.Store.UpsertReport(ctx, employeeID, from, statsJSON, summaryMD, d.LLM.Model)
	if err != nil {
		return fmt.Errorf("upsert report: %w", err)
	}

	if d.Sender != nil {
		emp, err := d.Store.GetEmployee(ctx, employeeID)
		if err != nil {
			return fmt.Errorf("get employee: %w", err)
		}
		if emp.FeishuOpenID != "" {
			text := renderReportText(emp.Name, from, to, summaryMD)
			if err := d.Sender.SendText(ctx, emp.FeishuOpenID, text); err != nil {
				return fmt.Errorf("push report: %w", err)
			}
		}
		if err := d.Store.MarkReportPushed(ctx, id); err != nil {
			return fmt.Errorf("mark pushed: %w", err)
		}
	}
	return nil
}

// joinTurns renders a session's turns as plain text for the model.
func joinTurns(turns []Turn) string {
	var b strings.Builder
	for _, t := range turns {
		b.WriteString(t.Role)
		b.WriteString(": ")
		b.WriteString(t.Text)
		b.WriteString("\n")
	}
	return b.String()
}

// renderReportText builds the message sent to the employee: the draft alone,
// ready to copy. Usage numbers stay on the portal, out of the draft.
func renderReportText(name string, from, to time.Time, summaryMD string) string {
	var b strings.Builder
	if name != "" {
		b.WriteString(name)
		b.WriteString("，")
	}
	fmt.Fprintf(&b, "本周周报已帮你写好（%s–%s），复制后稍作修改就能用：\n\n", from.Format("01/02"), to.AddDate(0, 0, -1).Format("01/02"))
	b.WriteString(strings.TrimSpace(summaryMD))
	b.WriteString("\n\n———\n这份周报草稿根据你本周在 Codex 里做的事整理，只发给你本人。不需要的话，可以在门户「我的周报」里关掉。")
	return b.String()
}
