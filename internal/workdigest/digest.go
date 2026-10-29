package workdigest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"aiseat/internal/settings"
	"aiseat/internal/store"
)

// Prompts. Summaries say what was worked on and how far it got, from the
// material only; they do not grade people.
const (
	employeePrompt = `你在帮公司管理员了解员工每天用 AI 编程助手（Codex）做了哪些工作。下面是某位员工某一天在 Codex 里发出的消息，按会话分组；每个会话后附有助手最后一次回复的开头，用来判断进展。
请用中文写这位员工当天的工作总结，Markdown 格式：
- 第一行用一句话概括当天主要在做什么；
- 然后按事项分条列出（每条一句：做的是什么、进展到哪一步），同一件事的多个会话合并，最多 8 条；
- 如果有明显卡住或反复出错的问题，最后单独一条写出来。
只根据材料写，不要编造；不要评价工作量、效率或态度；不要照抄代码、日志或系统提示。`

	overviewPrompt = `下面是团队每位成员某一天的工作总结。请用中文给管理员写当日团队工作概览，Markdown 格式：
1. 一段话概括团队当天的主要工作方向；
2. 按人列出各自在做什么（每人一行，以名字开头）；
3. 如果几个人在做相关的事情，指出可以协同的地方；如果有共同的卡点，指出来。
只根据材料写，不要编造，不要对个人做评价或排名。`
)

// callTimeout bounds one summary call.
const callTimeout = 3 * time.Minute

var (
	ErrNotConfigured = errors.New("还没选用于总结的账号：到「设置 → 每日工作总结」里选一个 ChatGPT 账号")
	ErrBusy          = errors.New("正在生成总结，请稍后再试")
)

// Runner writes the daily summaries, one run at a time.
type Runner struct {
	Store      *store.Store
	Settings   *settings.Service
	CPABaseURL string
	CPAAPIKey  string
	HC         *http.Client // optional
	Loc        *time.Location
	Log        *slog.Logger
	// Sender pushes the Friday admin briefings; nil stores them without
	// pushing (Feishu not configured).
	Sender Sender

	mu      sync.Mutex
	running bool
	// briefRunning tracks per-admin briefing runs (Friday job, previews).
	briefMu      sync.Mutex
	briefRunning map[int64]briefingPeriod
}

// Day is local midnight of t's date.
func (r *Runner) Day(t time.Time) time.Time {
	loc := r.loc()
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// Start checks the setup and summarizes day in the background.
func (r *Runner) Start(day time.Time) error {
	if _, _, err := r.client(context.Background()); err != nil {
		return err
	}
	if !r.acquire() {
		return ErrBusy
	}
	if err := r.Store.StartWorkDigestDay(context.Background(), r.Day(day)); err != nil {
		r.release()
		return err
	}
	go func() {
		defer r.release()
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		if err := r.run(ctx, day); err != nil {
			r.logger().Warn("work digest", "day", day.Format(time.DateOnly), "err", err)
		}
	}()
	return nil
}

// RunDay summarizes day and waits for it. Without an account set up it does
// nothing.
func (r *Runner) RunDay(ctx context.Context, day time.Time) error {
	if !r.acquire() {
		return ErrBusy
	}
	defer r.release()
	err := r.run(ctx, day)
	if errors.Is(err, ErrNotConfigured) {
		return nil
	}
	return err
}

// Running reports whether a run is under way in this process.
func (r *Runner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

func (r *Runner) acquire() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return false
	}
	r.running = true
	return true
}

func (r *Runner) release() {
	r.mu.Lock()
	r.running = false
	r.mu.Unlock()
}

// client builds the LLM from the settings: the chosen account and model.
func (r *Runner) client(ctx context.Context) (llm, string, error) {
	set := r.Settings.Get(ctx)
	if set.WorkDigestAccountID == 0 {
		return llm{}, "", ErrNotConfigured
	}
	acc, err := r.Store.GetAccount(ctx, set.WorkDigestAccountID)
	if err != nil {
		return llm{}, "", fmt.Errorf("总结用的账号找不到了（编号 %d），请到设置里重新选", set.WorkDigestAccountID)
	}
	if !acc.IsChatGPT() || acc.Status != "active" || acc.NeedsLogin {
		return llm{}, "", fmt.Errorf("总结用的账号 %s 现在不可用，请到设置里换一个", acc.Email)
	}
	model := strings.TrimSpace(set.WorkDigestModel)
	if model == "" {
		model = set.DefaultCodexModel
	}
	hc := r.HC
	if hc == nil {
		hc = &http.Client{Timeout: callTimeout}
	}
	return llm{
		baseURL: strings.TrimRight(r.CPABaseURL, "/"), apiKey: r.CPAAPIKey,
		model: acc.Prefix() + "/" + model, profile: set.CodexProfile, device: acc.DeviceProfile, hc: hc,
	}, model, nil
}

// run summarizes each employee's day, then the team's. One employee's
// failure does not stop the others.
func (r *Runner) run(ctx context.Context, day time.Time) error {
	day = r.Day(day)
	l, model, err := r.client(ctx)
	if err != nil {
		return err
	}
	from, to := day, day.AddDate(0, 0, 1)
	if now := time.Now(); to.After(now) {
		to = now
	}
	if err := r.Store.StartWorkDigestDay(ctx, day); err != nil {
		return err
	}
	finish := func(overview, status, msg string) error {
		// Record the outcome even if ctx ran out.
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return r.Store.FinishWorkDigestDay(fctx, day, overview, model, status, msg)
	}

	ids, err := r.Store.TextEmployees(ctx, from, to)
	if err != nil {
		_ = finish("", "failed", err.Error())
		return err
	}
	var kept []int64
	var lines []string // "name：summary" for the overview
	var failed []string
	for _, id := range ids {
		emp, err := r.Store.GetEmployee(ctx, id)
		if err != nil {
			failed = append(failed, fmt.Sprintf("员工 %d：%v", id, err))
			continue
		}
		summary, ok, err := r.employee(ctx, l, model, day, from, to, emp)
		if err != nil {
			failed = append(failed, fmt.Sprintf("%s：%v", emp.Name, err))
			kept = append(kept, id) // keep an earlier summary, if any
			continue
		}
		if !ok {
			continue
		}
		kept = append(kept, id)
		lines = append(lines, "### "+emp.Name+"\n"+summary)
	}
	if err := r.Store.PruneWorkDigests(ctx, day, kept); err != nil {
		r.logger().Warn("work digest: prune", "err", err)
	}

	msg := ""
	if len(failed) > 0 {
		msg = fmt.Sprintf("%d 人没能生成：%s", len(failed), strings.Join(failed, "；"))
	}
	if len(lines) == 0 {
		if len(failed) > 0 {
			_ = finish("", "failed", msg)
			return errors.New(msg)
		}
		return finish("", "done", "")
	}

	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	overview, err := l.complete(cctx, overviewPrompt,
		fmt.Sprintf("日期：%s\n\n%s", day.Format("2006-01-02"), strings.Join(lines, "\n\n")))
	cancel()
	if err != nil {
		msg = strings.TrimLeft(msg+"；团队概览没能生成："+err.Error(), "；")
		_ = finish("", "failed", msg)
		return err
	}
	return finish(overview, "done", msg)
}

// employee writes and stores one employee's summary; ok is false when the
// day holds nothing they typed.
func (r *Runner) employee(ctx context.Context, l llm, model string, day, from, to time.Time, emp store.Employee) (string, bool, error) {
	turns, err := r.Store.EmployeeTexts(ctx, emp.ID, from, to)
	if err != nil {
		return "", false, err
	}
	m := BuildMaterial(turns, r.loc())
	if m.Text == "" {
		return "", false, nil
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	summary, err := l.complete(cctx, employeePrompt,
		fmt.Sprintf("员工：%s\n日期：%s\n\n%s", emp.Name, day.Format("2006-01-02"), m.Text))
	if err != nil {
		return "", false, err
	}
	if err := r.Store.SaveWorkDigest(ctx, store.WorkDigest{
		Day: day, EmployeeID: emp.ID, SummaryMD: summary, Sessions: m.Sessions, Messages: m.Messages, Model: model,
	}); err != nil {
		return "", false, err
	}
	return summary, true, nil
}

func (r *Runner) loc() *time.Location {
	if r.Loc != nil {
		return r.Loc
	}
	return time.Local
}

func (r *Runner) logger() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}
