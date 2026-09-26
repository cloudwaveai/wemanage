package report

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"aiseat/internal/store"
)

// System prompts. The LLM writes only prose; it never supplies numbers.
const (
	sessionSystemPrompt = "你是工作助理，用一两句中文概括这次对话做了什么"
	dailySystemPrompt   = "根据这些会话概要，用中文写一段简短的每日工作小结，包含：做了哪些项目/任务、遇到的问题/卡点"
)

// maxSessionsPerReport caps how many sessions are summarized per employee.
const maxSessionsPerReport = 20

// Sender pushes a report to an employee. *feishu.Client satisfies this.
type Sender interface {
	SendText(ctx context.Context, openID, text string) error
	SendCard(ctx context.Context, openID string, card any) error
}

// Deps are the collaborators for generating daily reports.
type Deps struct {
	Store   *store.Store
	LLM     LLM
	Sender  Sender // may be nil to skip pushing
	Loc     *time.Location
	Enabled bool

	// LeaderOpenIDsForDept optionally maps an employee to their department
	// leaders for the aggregate card. It may be nil.
	LeaderOpenIDsForDept func(ctx context.Context, employeeID int64) []string
}

// GenerateForDate generates + stores + pushes reports for all employees active
// on date. If Enabled is false it is a no-op returning nil. A single employee's
// failure does not abort the batch; a combined error is returned only when
// every employee failed.
func GenerateForDate(ctx context.Context, d Deps, date time.Time) error {
	if !d.Enabled {
		return nil
	}
	loc := d.Loc
	if loc == nil {
		loc = time.UTC
	}
	from := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	to := from.Add(24 * time.Hour)

	ids, err := d.Store.ActiveEmployeesOn(ctx, from, to)
	if err != nil {
		return err
	}

	var errs []error
	ok := 0
	for _, empID := range ids {
		if err := generateOne(ctx, d, empID, date, loc); err != nil {
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

// generateOne collects, summarizes, stores, and pushes one employee's report.
func generateOne(ctx context.Context, d Deps, employeeID int64, date time.Time, loc *time.Location) error {
	mat, err := Collect(ctx, d.Store, employeeID, date, loc)
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

	summaryMD, err := d.LLM.Complete(ctx, dailySystemPrompt, strings.Join(sessionSummaries, "\n"))
	if err != nil {
		return fmt.Errorf("daily summary: %w", err)
	}

	// Numbers are taken from SQL (mat.Stats), never from the model output.
	statsJSON, err := json.Marshal(mat.Stats)
	if err != nil {
		return fmt.Errorf("marshal stats: %w", err)
	}
	id, err := d.Store.UpsertDailyReport(ctx, employeeID, date, statsJSON, summaryMD, d.LLM.Model)
	if err != nil {
		return fmt.Errorf("upsert report: %w", err)
	}

	if d.Sender != nil {
		emp, err := d.Store.GetEmployee(ctx, employeeID)
		if err != nil {
			return fmt.Errorf("get employee: %w", err)
		}
		if emp.FeishuOpenID != "" {
			text := renderReportText(emp.Name, date, summaryMD, mat.Stats)
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

// renderReportText builds the pushed message. All numbers come from Stats (SQL).
func renderReportText(name string, date time.Time, summaryMD string, s Stats) string {
	var b strings.Builder
	if name != "" {
		b.WriteString(name)
		b.WriteString(" ")
	}
	b.WriteString("AI 使用日报 ")
	b.WriteString(date.Format("2006-01-02"))
	b.WriteString("\n\n")
	b.WriteString(strings.TrimSpace(summaryMD))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "请求数：%d\n输入 tokens：%d\n输出 tokens：%d\n推理 tokens：%d",
		s.Requests, s.TokensInput, s.TokensOutput, s.TokensReasoning)
	return b.String()
}
