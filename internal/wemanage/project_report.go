package wemanage

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/http"
	"strings"
	"unicode/utf8"
)

const projectReportPrompt = `请根据提供的日报，反向还原指定项目的工作脉络。日报可能混合多个项目，只提取与目标项目明确有关的内容；不能确定归属的列为待确认。用中文 Markdown，输出以下部分：
## 项目目标：这个项目要解决什么问题，当前目标如何变化；没有依据就写尚不明确。
## 当前进展：截至最新来源日期，已完成、进行中、待验证分别是什么。助手提出方案不等于已经执行，代码修改不等于已经部署或验收。没有明确计划总量，不给进度百分比。
## 工作时间线：按日期从早到晚归纳关键动作、决定、实际产出和阶段变化，每项标注［来源 N］。重复材料合并，保留相互冲突的记录供人核对。
## 问题与下一步：区分明确阻塞、原记录待办与分析建议，不凭空指定负责人或截止日期。
## 可复用经验：列出适用场景、做法、验证方式及限制，标明知识或 Skill 候选。
不要评价个人或根据 Token 推断工作时间。每项关键结论都引用来源；输出标明是待人工核对的分析草稿。`

func (s *Service) reportProject(r *http.Request) (string, error) {
	if !strings.HasPrefix(r.URL.Path, "/api/project-report") {
		return "", nil
	}
	name := strings.TrimSpace(r.URL.Query().Get("project"))
	if name == "" || utf8.RuneCountInString(name) > 150 {
		return "", fmt.Errorf("invalid project")
	}
	var visible bool
	err := s.Store.Pool.QueryRow(r.Context(), `select not exists(select 1 from wm_project_candidate_states where name=$1 and hidden) and not exists(select 1 from projects p join wm_hidden_projects h on h.project_id=p.id where p.name=$1) and exists(select 1 from wm_knowledge_items i join wm_knowledge_runs run on run.id=i.run_id where run.status='done' and i.status<>'rejected' and coalesce((select canonical from wm_project_candidate_aliases where alias=trim(i.project_hint)),trim(i.project_hint))=$1 and `+projectClueVisible+`)`, name).Scan(&visible)
	if err != nil || !visible {
		return "", fmt.Errorf("invisible project")
	}
	return name, nil
}
func (s *Service) projectReportItems(ctx context.Context, tx pgx.Tx, name, from, to string) ([]int64, error) {
	rows, err := tx.Query(ctx, `select i.id from wm_knowledge_items i join wm_knowledge_runs run on run.id=i.run_id where run.status='done' and i.status<>'rejected' and coalesce((select canonical from wm_project_candidate_aliases where alias=trim(i.project_hint)),trim(i.project_hint))=$1 and exists(select 1 from wm_knowledge_sources src where src.run_id=i.run_id and src.source_day >= $2::date and src.source_day <= $3::date and exists(select 1 from jsonb_array_elements(i.evidence) ev where ev->>'source'=src.ordinal::text)) and `+projectClueVisible+` order by i.id`, name, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
