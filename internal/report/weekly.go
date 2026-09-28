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
// recap is for the employee, so it describes what AI helped with and never
// judges effort, output or hours.
const (
	sessionSystemPrompt = "用一两句中文概括这次对话借助 AI 做了什么。只写事情本身，不做任何评价。"
	weeklySystemPrompt  = "下面是一个人本周借助 AI 完成的事情概要。请用中文为他本人写一段简短、轻松友好的本周回顾：主要借助 AI 做了哪些事，有没有值得以后复用的做法。不要评价工作量、效率或投入时间，不要写成工作汇报。"
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
			text := renderReportText(emp.Name, from, to, summaryMD, mat.Stats)
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

// renderReportText builds the message sent to the employee. All numbers come
// from Stats (SQL).
func renderReportText(name string, from, to time.Time, summaryMD string, s Stats) string {
	var b strings.Builder
	if name != "" {
		b.WriteString(name)
		b.WriteString("，")
	}
	fmt.Fprintf(&b, "这是你本周的 AI 使用回顾（%s–%s）\n\n", from.Format("01/02"), to.AddDate(0, 0, -1).Format("01/02"))
	b.WriteString(strings.TrimSpace(summaryMD))
	fmt.Fprintf(&b, "\n\n本周 %d 次请求，输入 %d / 输出 %d tokens。", s.Requests, s.TokensInput, s.TokensOutput)
	b.WriteString("\n\n这份回顾只发给你本人。不想收到的话，可以在门户「我的周报」里关掉。")
	return b.String()
}
