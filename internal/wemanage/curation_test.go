package wemanage

import (
	"encoding/json"
	"testing"
)

func TestCurationRejectsInventedEvidenceAndMissingCoverage(t *testing.T) {
	sources := []*projectCandidate{{Name: "项目甲", Clues: []projectClue{{ID: 1, Title: "发布检查流程", Content: "先保存日志再检查结果"}}}, {Name: "甲项目", Clues: []projectClue{{ID: 2, Content: "先保存日志再检查结果"}}}}
	good := curationResult{Merges: []curationMerge{{Name: "甲项目", Target: "项目甲", Reason: "明确相同项目", Evidence: []curationEvidence{{1, "先保存日志"}, {2, "先保存日志"}}}}, Items: []curationItem{{Source: 1, Title: "检查日志", Kind: "handbook", Disposition: "new", Reason: "保留做法", Related: []int64{2}, Content: "先保存日志再检查结果", Evidence: []curationEvidence{{1, "先保存日志"}, {2, "先保存日志"}}}}}
	if err := validateCuration(good, sources); err != nil {
		t.Fatal(err)
	}
	good.Merges[0].Evidence[0].Quote = "发布检查流程"
	if err := validateCuration(good, sources); err != nil {
		t.Fatal("valid title evidence rejected", err)
	}
	clone := func() curationResult {
		b, _ := json.Marshal(good)
		var out curationResult
		_ = json.Unmarshal(b, &out)
		return out
	}
	for name, mutate := range map[string]func(*curationResult){
		"invented quote":  func(r *curationResult) { r.Items[0].Evidence[0].Quote = "不存在的文字" },
		"uncovered clue":  func(r *curationResult) { r.Items[0].Related = nil },
		"one-sided merge": func(r *curationResult) { r.Merges[0].Evidence = r.Merges[0].Evidence[:1] },
		"cycle": func(r *curationResult) {
			m := r.Merges[0]
			m.Name, m.Target = m.Target, m.Name
			r.Merges = append(r.Merges, m)
		},
		"invalid flow": func(r *curationResult) {
			r.Items[0].Kind = "skill"
			r.Items[0].Flow = []knowledgeNode{{ID: "S1", Text: "操作", Next: []knowledgeEdge{{Condition: "完成", Target: "unknown"}}}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := clone()
			mutate(&r)
			if validateCuration(r, sources) == nil {
				t.Fatal("invalid result accepted")
			}
		})
	}
	if !curationInputsVisible(sources, append(sources, &projectCandidate{Name: "新增项目"})) {
		t.Fatal("new data hid old result")
	}
	current := []*projectCandidate{sources[0]}
	if curationInputsVisible(sources, current) {
		t.Fatal("private source allowed")
	}
}
