package workdigest

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"aiseat/internal/store"
)

// The admin weekly briefing. Every Friday each super admin who keeps it on
// gets one briefing for the week so far, written under their own prompt
// (their focus) from the week's daily summaries and usage numbers. The same
// flow powers the portal's "test with this week's data" button.

// DefaultBriefingPrompt is the system prompt for admins who have not written
// their own. It aims at the usual management lens: what moved, what is
// stuck, what needs attention, plus a short usage footnote.
const DefaultBriefingPrompt = `你是一位务实的技术团队管理助手。下面是团队在所选日期范围内的每日工作总结（团队每日概览、每人每天的详细记录、逐日用量和所选周期用量统计表）。请为团队管理员写一份所选周期工作简报，中文，Markdown 格式：

## 所选周期概览
用一段话概括团队所选周期的主要工作方向和整体进展（3-5 句）。

## 关键进展
按主题（项目或工作方向）归纳所选周期推进或完成的事项，同主题的多人工作合并为一条并注明涉及的人；每条一两句，写清楚进展到了哪一步。

## 风险与卡点
列出材料中明确提到的阻塞、反复出错或长时间没有进展的事项，注明涉及的人和材料中的依据；没有就写"所选周期未见明显风险"。

## 需要关注的情况
只对材料中有明确证据的异常做客观提示（例如某人连续多日卡住、整个周期没有产出记录），并引用材料中的事实；没有就写"无"。

## 协同建议
材料显示多人在做相关事情时，指出可以怎样配合；没有就省略本节。

## 用量小结
根据文末的统计表，用两三句话概括团队整体用量：总请求、输入/输出 Token、活跃人数、最常用的模型。不要罗列每个人的数字。

写作要求：
- 只根据给定材料写作，不得编造材料中没有的事项、数字或结论；
- 所有数字只能来自材料中的统计表，不要自己估算；
- 不要评价员工的工作量、效率或态度，不给个人打分、不排名；
- 语言简洁、书面化，日期用"X月X日"；
- 材料没有覆盖的天数或细节不要提及。`

// Material budgets. A week of daily summaries is far larger than one day;
// the cap keeps one call readable, and trimming drops the oldest days' per
// person detail first (overviews and usage always stay).
const (
	maxBriefingMaterialRunes = 100_000
	maxBriefingOverviewRunes = 3_000
	maxBriefingDigestRunes   = 2_000
	minBriefingDetailDays    = 2
	maxBriefingUsageRows     = 40
)

var (
	ErrBriefingBusy       = errors.New("正在生成，请稍后再试")
	ErrNoBriefingMaterial = errors.New("所选日期内没有可用于简报的工作内容；已检查并补齐每日总结")
)

// Sender pushes a briefing to an admin. *feishu.Client satisfies this.
type Sender interface {
	SendText(ctx context.Context, openID, text string) error
}

var weekdayCN = []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

// Per-admin briefing locks, so a Friday job, a preview and a manual send for
// the same person never interleave.
type briefingPeriod struct{ from, to time.Time }

func (r *Runner) acquireBriefing(empID int64, from, to time.Time) bool {
	r.briefMu.Lock()
	defer r.briefMu.Unlock()
	if r.briefRunning == nil {
		r.briefRunning = map[int64]briefingPeriod{}
	}
	if _, ok := r.briefRunning[empID]; ok {
		return false
	}
	r.briefRunning[empID] = briefingPeriod{from, to}
	return true
}

func (r *Runner) releaseBriefing(empID int64) {
	r.briefMu.Lock()
	delete(r.briefRunning, empID)
	r.briefMu.Unlock()
}

// BriefingRunning reports whether this process is generating that admin's
// briefing right now (for displaying a stuck "running" row as interrupted).
func (r *Runner) BriefingRunning(empID int64) bool {
	r.briefMu.Lock()
	defer r.briefMu.Unlock()
	_, ok := r.briefRunning[empID]
	return ok
}

// BriefingRangeRunning distinguishes an active range from interrupted history.
func (r *Runner) BriefingRangeRunning(empID int64, from, to time.Time) bool {
	r.briefMu.Lock()
	defer r.briefMu.Unlock()
	period, ok := r.briefRunning[empID]
	return ok && period.from.Format(time.DateOnly) == from.Format(time.DateOnly) && period.to.Format(time.DateOnly) == to.Format(time.DateOnly)
}

// WeekBriefingWindow is the span the Friday briefing covers: Monday 00:00 to
// today 00:00 in the company timezone — the days the daily summaries have
// already written up by Friday morning.
func (r *Runner) WeekBriefingWindow(now time.Time) (from, to time.Time) {
	loc := r.loc()
	t := now.In(loc)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	offset := (int(day.Weekday()) + 6) % 7
	return day.AddDate(0, 0, -offset), day
}

// resolveBriefingPrompt falls back to the default when the admin saved none.
func resolveBriefingPrompt(saved string) string {
	if p := strings.TrimSpace(saved); p != "" {
		return p
	}
	return DefaultBriefingPrompt
}

// briefingMaterial assembles the week's daily summaries and usage numbers
// into the text the model reads. ok is false when the week holds no finished
// daily summary with any content.
func (r *Runner) briefingMaterial(ctx context.Context, from, to time.Time) (string, bool, error) {
	loc := r.loc()
	days, err := r.Store.WorkDigestDaysInRange(ctx, from, to)
	if err != nil {
		return "", false, err
	}
	names := map[int64]store.EmployeeRow{}
	if emps, err := r.Store.ListEmployees(ctx, store.EmployeeFilter{}); err == nil {
		for _, e := range emps {
			names[e.ID] = e
		}
	}

	// One chunk per (day, employee) digest; the overview is a separate list.
	type chunk struct {
		day   time.Time
		empID int64
		text  string
	}
	var chunks []chunk
	var overviewB strings.Builder
	covered := 0
	for _, d := range days {
		if d.Status != "done" {
			continue
		}
		digests, err := r.Store.WorkDigests(ctx, d.Day)
		if err != nil {
			return "", false, err
		}
		dayStr := d.Day.In(loc).Format("01-02")
		wd := weekdayCN[d.Day.In(loc).Weekday()]
		overview := strings.TrimSpace(d.OverviewMD)
		wrote := false
		if overview != "" {
			fmt.Fprintf(&overviewB, "## %s %s\n%s\n\n", dayStr, wd, truncate(overview, maxBriefingOverviewRunes))
			wrote = true
		}
		for _, g := range digests {
			e, ok := names[g.EmployeeID]
			if ok && e.IsService {
				continue
			}
			md := strings.TrimSpace(g.SummaryMD)
			if md == "" {
				continue
			}
			chunks = append(chunks, chunk{d.Day, g.EmployeeID,
				fmt.Sprintf("### %s %s\n%s\n", dayStr, wd, truncate(md, maxBriefingDigestRunes))})
			wrote = true
		}
		if wrote {
			covered++
		}
	}
	if covered == 0 || len(chunks) == 0 {
		return "", false, nil
	}

	// Usage: per-day totals and per-employee for the window, from SQL only.
	usage, err := r.Store.Usage(ctx, from, to, nil)
	if err != nil {
		return "", false, err
	}
	work, err := r.Store.WorkByEmployee(ctx, from, to, loc)
	if err != nil {
		return "", false, err
	}
	dayOrigin := time.Date(from.In(loc).Year(), from.In(loc).Month(), from.In(loc).Day(), 0, 0, 0, 0, loc)
	series, err := r.Store.UsageSeries(ctx, from, to, nil, 24*time.Hour, dayOrigin)
	if err != nil {
		return "", false, err
	}

	// Trim the per-person detail, oldest days first, until the whole message
	// fits; overviews, per-day totals and the usage table always stay.
	empName := func(id int64) string {
		if e, ok := names[id]; ok && e.Name != "" {
			return e.Name
		}
		return fmt.Sprintf("员工%d", id)
	}
	dayKey := func(t time.Time) string { return t.In(loc).Format("2006-01-02") }
	byDay := map[string][]chunk{}
	var dayOrder []string
	for _, c := range chunks {
		k := dayKey(c.day)
		if _, ok := byDay[k]; !ok {
			dayOrder = append(dayOrder, k)
		}
		byDay[k] = append(byDay[k], c)
	}
	trimNote := ""
	empSectionRunes := func(keep map[string]bool) int {
		n := 0
		for k, cs := range byDay {
			if !keep[k] {
				continue
			}
			for _, c := range cs {
				n += len([]rune(c.text))
			}
		}
		return n
	}
	keep := map[string]bool{}
	for _, k := range dayOrder {
		keep[k] = true
	}
	fixedRunes := len([]rune(overviewB.String())) + maxBriefingUsageRows*40 // table approx
	budget := maxBriefingMaterialRunes - fixedRunes
	for len(dayOrder) > minBriefingDetailDays && empSectionRunes(keep) > budget {
		delete(keep, dayOrder[0])
		dayOrder = dayOrder[1:]
		trimNote = "> 更早日期的逐人明细因篇幅省略，可在「工作看板」查看每天的完整总结。\n\n"
	}

	// Group the kept chunks by employee (name order), days inside each.
	empIDs := map[int64]bool{}
	var empOrder []int64
	for _, c := range chunks {
		if keep[dayKey(c.day)] && !empIDs[c.empID] {
			empIDs[c.empID] = true
			empOrder = append(empOrder, c.empID)
		}
	}
	sort.SliceStable(empOrder, func(i, j int) bool { return empName(empOrder[i]) < empName(empOrder[j]) })
	empChunks := map[int64][]chunk{}
	for _, c := range chunks {
		if keep[dayKey(c.day)] {
			empChunks[c.empID] = append(empChunks[c.empID], c)
		}
	}
	var empB strings.Builder
	empB.WriteString(trimNote)
	for _, id := range empOrder {
		fmt.Fprintf(&empB, "## %s\n", empName(id))
		cs := empChunks[id]
		sort.SliceStable(cs, func(i, j int) bool { return cs[i].day.Before(cs[j].day) })
		for _, c := range cs {
			empB.WriteString(c.text)
			empB.WriteString("\n")
		}
	}

	usageB := strings.Builder{}
	usageB.WriteString("# 逐日用量（系统统计）\n")
	for _, p := range series {
		if p.Requests == 0 && p.Tokens == 0 {
			continue
		}
		fmt.Fprintf(&usageB, "- %s %s：请求 %s，Token 合计 %s\n",
			p.T.In(loc).Format("01-02"), weekdayCN[p.T.In(loc).Weekday()], nfmt(p.Requests), nfmt(p.Tokens))
	}
	fmt.Fprintf(&usageB, "\n团队合计：请求 %s，输入 %s，输出 %s，活跃 %d 人\n\n",
		nfmt(usage.Requests), nfmt(usage.TokensInput), nfmt(usage.TokensOutput), len(work))
	usageB.WriteString("# 所选周期用量统计（系统统计）\n\n员工 | 请求 | 输入 | 输出 | 活跃天数 | 常用模型\n--- | --- | --- | --- | --- | ---\n")
	type uw struct {
		id    int64
		w     store.EmployeeWork
		total int64
	}
	var rows []uw
	for id, w := range work {
		if e, ok := names[id]; ok && e.IsService {
			continue
		}
		rows = append(rows, uw{id, w, w.TokensInput + w.TokensOutput})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].total > rows[j].total })
	if len(rows) > maxBriefingUsageRows {
		rows = rows[:maxBriefingUsageRows]
	}
	for _, row := range rows {
		fmt.Fprintf(&usageB, "%s | %s | %s | %s | %d | %s\n",
			empName(row.id), nfmt(row.w.Requests), nfmt(row.w.TokensInput), nfmt(row.w.TokensOutput),
			row.w.ActiveDays, row.w.TopModel)
	}

	lastDay := to.In(loc).AddDate(0, 0, -1)
	var b strings.Builder
	fmt.Fprintf(&b, "周期：%s 至 %s（%s），共 %d 天有每日总结。\n\n",
		from.In(loc).Format(time.DateOnly), lastDay.Format(time.DateOnly), weekdayCN[lastDay.Weekday()], covered)
	b.WriteString("# 团队每日概览\n\n")
	b.WriteString(overviewB.String())
	b.WriteString("# 每人所选周期工作（来自每日总结）\n\n")
	b.WriteString(empB.String())
	b.WriteString(usageB.String())
	return b.String(), true, nil
}

// generateBriefing writes one briefing: the admin's prompt over the material.
func (r *Runner) generateBriefing(ctx context.Context, prompt, material string) (string, string, error) {
	l, model, err := r.client(ctx)
	if err != nil {
		return "", "", err
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	content, err := l.complete(cctx, resolveBriefingPrompt(prompt), material)
	if err != nil {
		return "", model, err
	}
	return content, model, nil
}

// prepareBriefing fills missing or failed daily summaries before assembling
// material. Holding the daily lock also serializes backfills by different admins.
func (r *Runner) prepareBriefing(ctx context.Context, from, to time.Time) (string, error) {
	for !r.acquire() {
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
	defer r.release()
	for day := from; day.Before(to); day = day.AddDate(0, 0, 1) {
		d, err := r.Store.GetWorkDigestDay(ctx, day)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return "", err
		}
		// Today's summaries are provisional and must be refreshed.
		if err == nil && d.Status == "done" && d.Error == "" && d.FinishedAt != nil && !d.FinishedAt.Before(day.AddDate(0, 0, 1)) {
			continue
		}
		if err := r.run(ctx, day); err != nil {
			return "", fmt.Errorf("%s 的每日总结生成失败：%w", day.Format(time.DateOnly), err)
		}
		d, err = r.Store.GetWorkDigestDay(ctx, day)
		if err != nil {
			return "", err
		}
		if d.Error != "" {
			return "", fmt.Errorf("%s 的每日总结不完整：%s", day.Format(time.DateOnly), d.Error)
		}
	}
	material, ok, err := r.briefingMaterial(ctx, from, to)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNoBriefingMaterial
	}
	return material, nil
}

// runBriefing finishes a row already marked running by the caller.
func (r *Runner) runBriefing(ctx context.Context, emp store.Employee, prompt string, send bool,
	from, to time.Time, material string) error {
	finish := func(content, model, status, msg string, sent bool) error {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return r.Store.FinishAdminBriefing(fctx, emp.ID, from, to, content, model, status, msg, sent)
	}
	if material == "" {
		var err error
		material, err = r.prepareBriefing(ctx, from, to)
		if err != nil {
			_ = finish("", "", "failed", err.Error(), false)
			return err
		}
	}
	content, model, err := r.generateBriefing(ctx, prompt, material)
	if err != nil {
		_ = finish("", "", "failed", err.Error(), false)
		return err
	}
	if !send {
		return finish(content, model, "test", "", false)
	}
	if err := r.Sender.SendText(ctx, emp.FeishuOpenID, renderBriefingText(emp.Name, from, to, content)); err != nil {
		_ = finish(content, model, "failed", "简报已生成，飞书推送失败："+err.Error(), false)
		return err
	}
	return finish(content, model, "sent", "", true)
}

// StartBriefing persists the running state synchronously, then fills daily
// summaries and generates the selected date range in the background.
func (r *Runner) StartBriefing(ctx context.Context, emp store.Employee, prompt string, send bool, from, to time.Time) error {
	from, to = r.Day(from), r.Day(to)
	if !to.After(from) || to.After(from.AddDate(0, 0, 31)) || to.After(r.Day(time.Now()).AddDate(0, 0, 1)) {
		return errors.New("请选择有效的日期范围（最多 31 天，不能晚于今天）")
	}
	if _, _, err := r.client(ctx); err != nil {
		return err
	}
	if send && (r.Sender == nil || emp.FeishuOpenID == "") {
		return errors.New("没有配置飞书或当前账号未绑定飞书，请先生成简报在页面查看")
	}
	if !r.acquireBriefing(emp.ID, from, to) {
		return ErrBriefingBusy
	}
	if err := r.Store.StartAdminBriefing(ctx, emp.ID, from, to); err != nil {
		r.releaseBriefing(emp.ID)
		return err
	}
	go func() {
		defer r.releaseBriefing(emp.ID)
		cctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		if err := r.runBriefing(cctx, emp, prompt, send, from, to, ""); err != nil {
			r.logger().Warn("admin briefing", "employee", emp.ID, "err", err)
		}
	}()
	return nil
}

// RunWeekBriefings writes and pushes the week's briefing to every active
// super admin who keeps it on. It is a no-op when daily summaries are not
// configured or the week holds none yet; one admin's failure does not stop
// the others.
func (r *Runner) RunWeekBriefings(ctx context.Context, now time.Time) error {
	if _, _, err := r.client(ctx); err != nil {
		if errors.Is(err, ErrNotConfigured) {
			return nil
		}
		return err
	}
	from, to := r.WeekBriefingWindow(now)
	if !to.After(from) {
		return nil
	}
	material, err := r.prepareBriefing(ctx, from, to)
	if errors.Is(err, ErrNoBriefingMaterial) {
		return nil
	}
	if err != nil {
		return err
	}
	admins, err := r.Store.ListSuperAdmins(ctx)
	if err != nil {
		return err
	}
	var errs []string
	for _, admin := range admins {
		pref, found, err := r.Store.GetAdminBriefingPref(ctx, admin.ID)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s：%v", admin.Name, err))
			continue
		}
		if found && !pref.Enabled {
			continue
		}
		if !r.acquireBriefing(admin.ID, from, to) {
			errs = append(errs, fmt.Sprintf("%s：%v", admin.Name, ErrBriefingBusy))
			continue
		}
		err = r.Store.StartAdminBriefing(ctx, admin.ID, from, to)
		if err == nil {
			err = r.runBriefing(ctx, admin, pref.Prompt, r.Sender != nil && admin.FeishuOpenID != "", from, to, material)
		}
		r.releaseBriefing(admin.ID)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s：%v", admin.Name, err))
		}
	}
	if len(errs) > 0 {
		return errors.New("简报：" + strings.Join(errs, "；"))
	}
	return nil
}

// renderBriefingText builds the Feishu message: the briefing itself, with a
// pointer back to the portal where the admin tunes their prompt.
func renderBriefingText(name string, from, to time.Time, md string) string {
	var b strings.Builder
	if name != "" {
		b.WriteString(name)
		b.WriteString("，")
	}
	last := to.AddDate(0, 0, -1)
	span := from.Format("01/02")
	if lastDay := last.Format("01/02"); lastDay != from.Format("01/02") {
		span = fmt.Sprintf("%s–%s", from.Format("01/02"), lastDay)
	}
	fmt.Fprintf(&b, "团队工作简报（%s）：\n\n", span)
	b.WriteString(strings.TrimSpace(md))
	b.WriteString("\n\n———\n这份简报根据所选日期的每日工作总结整理。选择日期、调整侧重点或设置每周五自动推送，请到门户「简报」。")
	return b.String()
}

// nfmt renders an int64 with thousands separators for the usage tables.
func nfmt(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}
