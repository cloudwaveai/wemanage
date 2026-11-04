// Curated drafts; they remain reviewable and are never installed automatically.
package main

import (
	"aiseat/internal/db"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type draft struct {
	sourceTitle, day, variant, name, title, trigger, verify, limits string
	steps                                                           []string
	focus                                                           bool
}

var drafts = []draft{
	{sourceTitle: "PPT 工作台验收未通过，部分任务需返工", day: "2026-11-01", variant: "ppt-review", name: "ppt-rework-review", title: "PPT 返工与生成效果复核", trigger: "演示文稿生成或编辑功能返工后，需要判断问题是否修复、整稿是否达到验收条件。", steps: []string{"列出原失败场景、返工项和本轮验收口径，明确版本及基准。", "用反例和合法例走真实保存路径，核对数字、单位与原始事实；对异步图片任务验证用户修改保护。", "按实际统计对象独立重算正文与版式指标，并记录样本范围。", "分别检查生成终态、编辑画布、直接预览和导出渲染，记录失败与未验证项。"}, verify: "每项结论能对应复现输入、实际结果和可检查产物；分别说明通过、未通过和未知。", limits: "来自一次特定项目的日志深挖，模型引用尚未自动核实；不能将局部通过视为整稿或所有客户端验收通过。", focus: true},
	{sourceTitle: "基于对标文章证据提炼写作方法（SKILL.md 草稿）", day: "2026-10-26", variant: "writing", name: "evidence-based-writing-analysis", title: "从文章样本提炼写作方法", trigger: "已有可编号保存的对标文章与阅读数据，需要分析标题、结构或写法。", steps: []string{"分批接收文章与阅读数据，编号存档并保留原文依据。", "对照高低阅读样本，记录标题切入、故事感及结构差异。", "把有依据的观察整理为写作建议，并关联具体样本。", "把新文案与已验证的训练样本分开，不自动纳入方法库。"}, verify: "逐项核对建议是否能回溯到文章原文、阅读数据或修改证据。", limits: "样本少或整体拆解未完成时，只能提出待验证观察，不能宣称稳定规律。"},
	{sourceTitle: "初三数学月考错题分析与个性化辅导流程草稿", day: "2026-10-19", variant: "math", name: "junior-high-math-exam-analysis", title: "错题分析与个性化辅导", trigger: "教师提供试卷、答题纸或逐题得分，需要整理错题分析和辅导材料。", steps: []string{"逐题核对得分，并将合计与原标注总分比对，列明差异。", "汇总失分分布，依据实际作答分析代表性学生或题目，区分观察与推测。", "围绕明确题目或知识点制作分析报告和个性化练习。", "按教师反馈先确认样张，再整理其余材料；复测标准未知时标记待确认。", "交付前核对学生、题号、分数、总分与练习数量，并保留逐题依据。"}, verify: "核对逐题合计、结论依据、练习及复测标准；由教师审核样张和结果。", limits: "只适用于材料和评分依据可核对的场景，不替代教师判断；相关资料仍需按原有权限保护。"},
	{sourceTitle: "日报服务数据库切换的停写、迁移与核验顺序", day: "2026-10-26", variant: "migration", name: "database-migration-verification", title: "数据库切换前后核验", trigger: "已有数据库适配和迁移工具，准备将实际服务的数据切换到新数据库。", steps: []string{"先确认迁移工具与目标数据库连接可用，并检查隔离库验证结果。", "让旧服务停止写入，确定迁移窗口。", "执行数据迁移，并核验迁移结果。", "核验通过后再切换数据库，记录未解决的连接或认证问题。"}, verify: "检查旧服务停写、迁移完成和数据核验结果；分别记录认证错误与镜像构建状态。", limits: "当前来源只提供顺序和部分检查，具体迁移、备份与回退操作应依据项目方案；正式切换需遵循已有授权。"},
	{sourceTitle: "采集桌面端打包后分层验证", day: "2026-11-01", variant: "collection", name: "collector-release-validation", title: "采集工具分层验证", trigger: "桌面采集工具集成来源或优化性能后，准备判断发布包是否可用。", steps: []string{"运行离线测试，检查关键逻辑。", "执行前端构建、打包与自检，记录结果。", "单独验证目标网站上的真实采集。", "单独观察长期运行与性能表现，记录尚未覆盖的环境。"}, verify: "分别列出离线、构建、打包、真实网站和长期运行的结果，明确哪些尚未验证。", limits: "离线测试与打包自检不能证明真实网站或长期运行可靠；来源日报未给出具体测试脚本。"},
	{sourceTitle: "日报或工作简报中区分累计数据与功能状态", day: "2026-10-19", variant: "report-status", name: "report-evidence-status", title: "工作简报口径与功能状态核对", trigger: "需要整理产品工作简报，且同时出现统计数字、已部署功能和未来规划。", steps: []string{"确认每项数据的时间范围和统计口径，是当期还是累计。", "逐项区分已开发、已部署、规划中和尚未开发。", "为尚未开发的功能写清规划边界与审核流程。", "对照来源检查表述，避免把既有数据或计划写成已上线功能。"}, verify: "逐项核对数据口径和功能状态是否有来源支持，不确定的内容标记待确认。", limits: "现有候选的部分引文未自动匹配日报，需先核对原日报和相关工作记录。"},
}

func main() {
	ctx := context.Background()
	pool, err := db.Open(ctx, os.Getenv("DATABASE_URL"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "数据库不可用")
		os.Exit(1)
	}
	defer pool.Close()
	for _, d := range drafts {
		var item int64
		err = pool.QueryRow(ctx, `select i.id from wm_knowledge_items i join wm_knowledge_runs r on r.id=i.run_id where i.title=$1 and r.period_start=$2::date and r.status='done' and not exists(select 1 from wm_knowledge_sources s join employees e on e.id=s.employee_id where s.run_id=r.id and (e.name='林伊默' or e.private_work_since is not null)) order by r.version desc,i.id limit 1`, d.sourceTitle, d.day).Scan(&item)
		if err != nil {
			fmt.Fprintln(os.Stderr, "来源未就绪：", d.title)
			continue
		}
		var focus *int64
		if d.focus {
			var fid int64
			if err = pool.QueryRow(ctx, `select id from wm_focus_jobs where item_id=$1 and state='done' order by id desc limit 1`, item).Scan(&fid); err != nil {
				fmt.Fprintln(os.Stderr, "原始日志回查未就绪：", d.title)
				continue
			}
			focus = &fid
		}
		raw, _ := json.Marshal(d.steps)
		var b strings.Builder
		fmt.Fprintf(&b, "---\nname: %s\ndescription: %s\n---\n\n## 适用场景\n%s\n\n## 操作步骤\n", d.name, d.trigger, d.trigger)
		for i, step := range d.steps {
			fmt.Fprintf(&b, "%d. %s\n", i+1, step)
		}
		fmt.Fprintf(&b, "\n## 验证\n%s\n\n## 边界\n%s\n", d.verify, d.limits)
		tag, err := pool.Exec(ctx, `insert into wm_skill_drafts(source_item_id,variant,source_focus_id,title,trigger_text,steps,verification,limits_text,skill_md) values($1,$2,$3,$4,$5,$6,$7,$8,$9) on conflict(source_item_id,variant) do nothing`, item, d.variant, focus, d.title, d.trigger, raw, d.verify, d.limits, b.String())
		if err != nil {
			fmt.Fprintln(os.Stderr, "保存失败：", d.title)
			os.Exit(1)
		}
		fmt.Printf("%s：%s\n", d.title, map[bool]string{true: "已创建", false: "已存在"}[tag.RowsAffected() == 1])
	}
}
