package workrecords

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"image"
	"image/png"
	"testing"
)

func TestScanInlineImagesPreservesOriginalBytesAndOffsets(t *testing.T) {
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	png := buffer.Bytes()
	encoded := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	source := []byte(`{"role":"user","content":[{"type":"input_image","image_url":"` + encoded + `"},{"type":"input_text","text":"说明"}]}`)
	refs, err := ScanInlineImages(source)
	if err != nil || len(refs) != 1 {
		t.Fatalf("refs=%d %v", len(refs), err)
	}
	ref := refs[0]
	if !bytes.Equal(source[ref.Start:ref.End], []byte(encoded)) || ref.MIME != "image/png" || ref.Bytes != len(png) || ref.Digest != sha256.Sum256(png) {
		t.Fatalf("image range or original bytes changed: %+v", ref)
	}
}
func TestScanInlineImagesFailsClosedOnMalformedOrUnsupportedMedia(t *testing.T) {
	for _, source := range []string{
		`{"image":"data:image/png;base64,YWJj"}`,
		`{"image":"data:image/svg+xml;base64,PGc+"}`,
		`{"image":"data:image/png;base64,%%%"}`,
		`{"image":"data:image/png;base64,iVBORw0KGgo=-bad"}`,
	} {
		if _, err := ScanInlineImages([]byte(source)); err == nil {
			t.Fatalf("accepted invalid input %q", source)
		}
	}
}

func TestEscapedImageDoesNotBypassMediaGuard(t *testing.T) {
	for _, source := range []string{`{"image":"data:image\/png;base64,abc"}`, `{"image":"data:image\u002fpng;base64,abc"}`} {
		if !containsEmbeddedMedia([]byte(source)) {
			t.Fatal("escaped media bypassed guard")
		}
		if _, err := ScanInlineImages([]byte(source)); err == nil {
			t.Fatal("escaped media silently ignored")
		}
	}
}
