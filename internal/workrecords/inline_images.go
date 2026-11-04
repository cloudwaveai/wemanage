package workrecords

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"regexp"
	"strings"
)

// InlineImageRef identifies an original data URL without changing its encoded
// bytes or image quality. Offsets are into the immutable source blob.
type InlineImageRef struct {
	Start  int
	End    int
	MIME   string
	Bytes  int
	Digest [32]byte
}

var inlineImagePattern = regexp.MustCompile(`(?i)data:image/(png|jpeg|jpg|gif|webp);base64,[A-Za-z0-9+/]*={0,2}`)

// ScanInlineImages returns exact source ranges for supported image data with a valid header
// URLs. A malformed or unsupported image causes an error rather than being
// discarded or mistaken for analyzed text.
func ScanInlineImages(source []byte) ([]InlineImageRef, error) {
	lower := bytes.ToLower(source)
	if bytes.Contains(lower, []byte(`data:image\/`)) || bytes.Contains(lower, []byte(`data:image\u002f`)) {
		return nil, errors.New("escaped inline image requires explicit decoding")
	}
	matches := inlineImagePattern.FindAllSubmatchIndex(source, -1)
	if bytes.Count(bytes.ToLower(source), []byte("data:image/")) != len(matches) {
		return nil, errors.New("unsupported or malformed inline image")
	}
	refs := make([]InlineImageRef, 0, len(matches))
	for _, m := range matches {
		if m[1] < len(source) && !bytes.ContainsRune([]byte("\"' \t\r\n),]}<"), rune(source[m[1]])) {
			return nil, errors.New("ambiguous image data URL boundary")
		}
		mime := strings.ToLower(string(source[m[2]:m[3]]))
		url := source[m[0]:m[1]]
		comma := bytes.IndexByte(url, ',')
		if comma < 0 {
			return nil, errors.New("invalid image data URL")
		}
		encoded := url[comma+1:]
		decoded, err := base64.StdEncoding.DecodeString(string(encoded))
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(string(encoded))
		}
		if err != nil || len(decoded) == 0 {
			return nil, errors.New("invalid image data")
		}
		if mime == "jpg" {
			mime = "jpeg"
		}
		if mime == "webp" {
			return nil, errors.New("webp validation not configured")
		}
		config, format, err := image.DecodeConfig(bytes.NewReader(decoded))
		if err != nil || format != mime || config.Width <= 0 || config.Height <= 0 {
			return nil, errors.New("image data does not match its format")
		}
		refs = append(refs, InlineImageRef{Start: m[0], End: m[1], MIME: "image/" + mime, Bytes: len(decoded), Digest: sha256.Sum256(decoded)})
	}
	return refs, nil
}
