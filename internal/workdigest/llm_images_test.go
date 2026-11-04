package workdigest

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestImagesAreSentUnchangedAndUsageRecorded(t *testing.T) {
	const original = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAE="
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Messages []struct {
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
			return
		}
		var content []struct {
			Type     string `json:"type"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
		}
		if len(body.Messages) != 2 {
			t.Error("missing messages")
			return
		}
		if e := json.Unmarshal(body.Messages[1].Content, &content); e != nil {
			t.Error(e)
			return
		}
		if len(content) != 2 || content[0].Type != "text" || content[1].Type != "image_url" || content[1].ImageURL.URL != original {
			t.Error("image or context altered")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"图片描述"}}],"usage":{"prompt_tokens":100,"completion_tokens":12}}`))
	}))
	defer server.Close()
	client := llm{baseURL: server.URL, model: "sample", hc: server.Client()}
	text, usage, err := client.completeWithImages(context.Background(), "system", "source", []string{original})
	if err != nil || text != "图片描述" || usage.Input == nil || *usage.Input != 100 || usage.Output == nil || *usage.Output != 12 || calls != 1 {
		t.Fatalf("result %q %+v %v calls=%d", text, usage, err, calls)
	}
	if _, _, err = client.completeWithImages(context.Background(), "system", "source", []string{"https://example.invalid/image"}); err == nil || calls != 1 {
		t.Fatal("accepted external image or made unexpected call")
	}
}
