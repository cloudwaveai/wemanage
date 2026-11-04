package wemanage

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestKnowledgeEvidenceAndFlowValidation(t *testing.T) {
	sources := []summarySource{{Day: "2026-11-01", Text: "修复已经完成，但没有上线。"}}
	ledger := knowledgeLedger{Facts: []knowledgeFact{{ID: "F1", Statement: "修复未上线", Date: "2026-11-01", Status: "current", Evidence: []knowledgeQuote{{Source: 1, Quote: "但没有上线"}}}}}
	if err := validateLedger(ledger, sources); err != nil {
		t.Fatal(err)
	}
	ledger.Facts[0].Evidence[0].Quote = "已经上线验证"
	if validateLedger(ledger, sources) == nil {
		t.Fatal("accepted invented quote")
	}
	ledger.Facts[0].Evidence[0].Quote = "但没有上线"
	k := projectKnowledge{Overview: "待验收", Skills: []knowledgeEntry{{Title: "验证流程", Body: "检查部署状态", Status: "candidate", FactIDs: []string{"F1"}, Flow: []knowledgeNode{{ID: "S1", Text: "检查", Next: []knowledgeEdge{{Condition: "失败", Target: "S2"}}}, {ID: "S2", Text: "停止并人工处理"}}}}}
	if err := validateKnowledge(k, ledger); err != nil {
		t.Fatal(err)
	}
	k.Skills[0].Flow[0].Next[0].Target = "absent"
	if validateKnowledge(k, ledger) == nil {
		t.Fatal("accepted broken branch")
	}
	k.Skills[0].Flow[0].Next[0].Target = "S2"
	k.Skills[0].FactIDs = []string{"F999"}
	if validateKnowledge(k, ledger) == nil {
		t.Fatal("accepted unknown fact")
	}
}
func TestKnowledgeBatchesPreserveAllUTF8Content(t *testing.T) {
	text := strings.Repeat("完整记录。", 28000) + "最后一条不能丢失"
	source := summarySource{Day: "2026-11-01", Name: "甲", Text: text}
	batches := knowledgeBatches([]summarySource{source})
	var combined strings.Builder
	header := "[来源 1] 2026-11-01 · 甲\n"
	for _, b := range batches {
		if !utf8.ValidString(b) {
			t.Fatal("split UTF8")
		}
		if !strings.HasPrefix(b, header) {
			t.Fatal("lost ordinal")
		}
		combined.WriteString(strings.TrimSuffix(strings.TrimPrefix(b, header), "\n\n"))
	}
	if combined.String() != text {
		t.Fatal("source content lost")
	}
}
