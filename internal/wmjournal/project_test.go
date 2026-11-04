package wmjournal

import (
	"strings"
	"testing"
)

func TestProjectPreservesReadableTextAcrossRanges(t *testing.T) {
	data := []byte(`{"type":"reasoning","summary":"分析完成","encrypted_content":"SECRETSECRET","tail":"后续步骤"}`)
	var got strings.Builder
	for start := 0; start < len(data); start += 17 {
		end := min(start+17, len(data))
		text, gap, err := Project(data, start, end)
		if err != nil || gap {
			t.Fatalf("projection %v %v", gap, err)
		}
		got.WriteString(text)
	}
	text := got.String()
	if strings.Contains(text, "SECRET") || !strings.Contains(text, "分析完成") || !strings.Contains(text, "后续步骤") {
		t.Fatal(text)
	}
}

func TestProjectMarksImagesAndKeepsSurroundingText(t *testing.T) {
	data := []byte(`{"text":"处理截图","url":"data:image/png;base64,QUJDREVGRw==","result":"完成"}`)
	text, gap, err := Project(data, 0, len(data))
	if err != nil || !gap || strings.Contains(text, "QUJD") || !strings.Contains(text, "处理截图") || !strings.Contains(text, "完成") {
		t.Fatalf("%q %v %v", text, gap, err)
	}
	_, gap, err = Project([]byte(`{"type":"input_image","image_url":"https://example.invalid/a.png"}`), 0, 999)
	if err == nil {
		t.Fatal("invalid range accepted")
	}
}

func TestNestedCipherAndLiteralFieldName(t *testing.T) {
	data := []byte(`{"text":"需要处理 encrypted_content 字段","nested":{"encrypted_content":"secret"},"more":[{"encrypted_content":"othersecret"}]}`)
	projected, _, err := Project(data, 0, len(data))
	if err != nil || strings.Contains(projected, "secret") || !strings.Contains(projected, "需要处理 encrypted_content 字段") {
		t.Fatalf("%q %v", projected, err)
	}
}

func TestMediaExamplesRemainTextAndSVGIsGap(t *testing.T) {
	for _, input := range []string{`{"text":"例子 data:image/png;base64,' + image"}`, `{"text":"data:image/png;base64,[YOUR_DATA]"}`} {
		text, gap, err := Project([]byte(input), 0, len(input))
		if err != nil || gap || text != input {
			t.Fatalf("example changed %q %v %v", text, gap, err)
		}
	}
	data := []byte(`{"text":"图示","url":"data:image/svg+xml,%3Csvg%3E%3C/svg%3E"}`)
	text, gap, err := Project(data, 0, len(data))
	if err != nil || !gap || strings.Contains(text, "%3Csvg") || !strings.Contains(text, "图示") {
		t.Fatalf("svg %q %v %v", text, gap, err)
	}
	unsafe := []byte(`{"text":"data:image/png;base64,'` + strings.Repeat("A", 300) + `"}`)
	if _, _, err = Project(unsafe, 0, len(unsafe)); err == nil {
		t.Fatal("accepted ambiguous long encoding")
	}
	truncated := []byte(`{"text":"data:image/png;base64,QUJDREVGRw==…后续文字"}`)
	projected, gap, err := Project(truncated, 0, len(truncated))
	if err != nil || !gap || strings.Contains(projected, "QUJD") || !strings.Contains(projected, "…后续文字") {
		t.Fatalf("truncated example %q %v %v", projected, gap, err)
	}
}
