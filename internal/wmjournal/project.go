// Package wmjournal implements the independent log-analysis worker.
package wmjournal

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"

	"github.com/tidwall/gjson"
)

// Span references an unreadable or non-text part of the unchanged source.
type Span struct {
	Start, End int
	Kind       string
}

var mediaURL = regexp.MustCompile(`(?i)data:(?:image|audio|video)(?:/|\\/|\\u002f)[a-z0-9.+-]+(?:;[a-z0-9=.+-]+)*,[a-z0-9+/=_-]+`)
var svgURL = regexp.MustCompile(`(?i)data:image(?:/|\\/|\\u002f)svg\+xml,(?:%[0-9a-f]{2}|[a-z0-9._~:/?#@!$&()*+,;=-])+`)
var longEncoded = regexp.MustCompile(`[A-Za-z0-9+/=_-]{256,}`)
var mediaStart = regexp.MustCompile(`(?i)data:(?:image|audio|video)(?:/|\\/|\\u002f)`)
var mediaDescriptor = regexp.MustCompile(`(?i)"type"\s*:\s*"(?:input_image|image_url|image|input_audio|audio|video|input_video)"`)

// MediaShape reports only structural counts, never log content.
func MediaShape(data []byte) (starts, complete, noComma, emptyPayload int) {
	ss := mediaStart.FindAllIndex(data, -1)
	starts = len(ss)
	complete = len(mediaURL.FindAllIndex(data, -1))
	for _, s := range ss {
		tail := data[s[1]:]
		limit := min(len(tail), 160)
		tail = tail[:limit]
		comma := bytes.IndexByte(tail, ',')
		quote := bytes.IndexAny(tail, "\"\r\n")
		if comma < 0 || (quote >= 0 && quote < comma) {
			noComma++
			continue
		}
		if comma+1 >= len(tail) || bytes.ContainsAny(tail[comma+1:comma+2], "\" \r\n") {
			emptyPayload++
		}
	}
	return
}

// UnmatchedMediaKinds summarizes only syntactic classes, never source text.
func UnmatchedMediaKinds(data []byte) map[string]int {
	out := map[string]int{}
	for _, at := range mediaStart.FindAllIndex(data, -1) {
		tail := data[at[1]:min(len(data), at[1]+256)]
		end := bytes.IndexByte(tail, ',')
		if end < 0 {
			out["no_comma"]++
			continue
		}
		header := tail[:end]
		kind := "other_header"
		switch {
		case bytes.HasPrefix(header, []byte("png;base64")):
			kind = "png_base64"
		case bytes.HasPrefix(header, []byte("jpeg;base64")):
			kind = "jpeg_base64"
		case bytes.HasPrefix(header, []byte("webp;base64")):
			kind = "webp_base64"
		case bytes.HasPrefix(header, []byte("svg+xml")):
			kind = "svg"
		case bytes.Contains(header, []byte("base64")):
			kind = "other_base64"
		}
		if end+1 >= len(tail) {
			kind += "_no_payload"
		} else {
			c := tail[end+1]
			switch {
			case (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
				kind += "_alnum"
			case c == '<':
				kind += "_placeholder"
			case c == '\\':
				kind += "_escaped"
				if end+2 < len(tail) {
					switch tail[end+2] {
					case '/':
						kind += "_slash"
					case '"':
						kind += "_quote"
					case '\\':
						kind += "_backslash"
						if end+4 < len(tail) {
							switch tail[end+4] {
							case '"':
								kind += "_quote"
							case '/':
								kind += "_slash"
							case '\\':
								kind += "_backslash"
							case 'u':
								kind += "_unicode"
							default:
								kind += "_other"
							}
						}
						if end+3 < len(tail) {
							switch tail[end+3] {
							case '"':
								kind += "_quote"
							case '\\':
								kind += "_backslash"
							case '/':
								kind += "_slash"
							default:
								kind += "_other"
							}
						}
					case 'u':
						kind += "_unicode"
					default:
						kind += "_other"
					}
				}
			case c == '[':
				kind += "_bracket"
			case c == '{':
				kind += "_brace"
			case c == '$':
				kind += "_variable"
			case c == '.':
				kind += "_ellipsis"
			case c == '%':
				kind += "_percent"
			case c == '\'':
				kind += "_single_quote"
				if end+2 < len(tail) {
					switch tail[end+2] {
					case ' ', '\t':
						kind += "_space"
					case '+':
						kind += "_plus"
					case '"':
						kind += "_quote"
					default:
						kind += "_other"
					}
				}
			default:
				kind += "_other"
			}
		}
		out[kind]++
	}
	return out
}

func MediaTermination(data []byte) map[string]int {
	out := map[string]int{}
	for _, m := range mediaURL.FindAllIndex(data, -1) {
		if m[1] >= len(data) {
			out["end_of_segment"]++
			continue
		}
		c := data[m[1]]
		kind := fmt.Sprintf("byte_%d", c)
		switch c {
		case '\\':
			kind = "backslash"
			if m[1]+1 < len(data) {
				switch data[m[1]+1] {
				case '\\':
					kind += "_backslash"
				case '"':
					kind += "_quote"
				case '/':
					kind += "_slash"
				case 'u':
					kind += "_unicode"
				default:
					kind += "_other"
				}
			}
		case '%':
			kind = "percent"
		case ',':
			kind = "comma"
		case '.':
			kind = "period"
		case '"':
			kind = "quote"
		case '\'':
			kind = "single_quote"
		case ' ', '\n', '\r', '\t':
			kind = "whitespace"
		}
		out[kind]++
	}
	return out
}

// UnreadableSpans identifies binary encoding and encrypted model reasoning.
// The original is never altered. Unsupported media descriptors are reported as
// a coverage gap even if no inline binary could be recovered.
func UnreadableSpans(data []byte) ([]Span, bool, error) {
	var spans []Span
	starts := mediaStart.FindAllIndex(data, -1)
	for _, start := range starts {
		at := start[0]
		tail := data[at:]
		match := mediaURL.FindIndex(tail)
		if match == nil || match[0] != 0 {
			match = svgURL.FindIndex(tail)
		}
		if match != nil && match[0] == 0 {
			end := at + match[1]
			if end < len(data) && !bytes.ContainsRune([]byte("\"' \t\r\n}]"), rune(data[end])) && !bytes.HasPrefix(data[end:], []byte("…")) {
				// A quote escaped in an embedded JSON string ends the URL.
				if !(data[end] == '\\' && end+1 < len(data) && data[end+1] == '"') {
					return nil, true, errors.New("unsupported media encoding")
				}
			}
			spans = append(spans, Span{at, end, "媒体编码"})
			continue
		}
		// Code examples can end the URL prefix with a quote or placeholder;
		// preserve them as text only when no long encoded run follows.
		headerEnd := min(len(data), start[1]+160)
		commaRel := bytes.IndexByte(data[start[1]:headerEnd], ',')
		if commaRel < 0 {
			return nil, true, errors.New("unrecognized media encoding")
		}
		payload := start[1] + commaRel + 1
		if payload >= len(data) {
			return nil, true, errors.New("unrecognized media encoding")
		}
		c := data[payload]
		literal := c == '\'' || c == '[' || c == '{' || c == '$' || c == '<' || c == '.'
		if c == '\\' {
			end := payload
			for end < len(data) && data[end] == '\\' {
				end++
			}
			literal = end-payload >= 2 && end < len(data) && data[end] == '"'
		}
		if !literal || longEncoded.Match(data[payload:]) {
			return nil, true, errors.New("unrecognized media encoding")
		}
	}
	mediaGap := len(spans) > 0 || mediaDescriptor.Match(data)
	validJSON := gjson.ValidBytes(data)
	if !validJSON && bytes.Contains(data, []byte(`"encrypted_content"`)) {
		return nil, mediaGap, errors.New("unrecognized encrypted source")
	}
	if validJSON {
		var parseErr error
		var visit func(gjson.Result)
		visit = func(node gjson.Result) {
			node.ForEach(func(key, field gjson.Result) bool {
				if key.String() == "encrypted_content" {
					if field.Type != gjson.String || field.Index < 0 || field.Index+len(field.Raw) > len(data) || !bytes.Equal(data[field.Index:field.Index+len(field.Raw)], []byte(field.Raw)) {
						parseErr = errors.New("invalid encrypted field")
						return false
					}
					spans = append(spans, Span{field.Index, field.Index + len(field.Raw), "加密推理"})
				} else if field.IsObject() || field.IsArray() {
					visit(field)
				}
				return parseErr == nil
			})
		}
		visit(gjson.ParseBytes(data))
		if parseErr != nil {
			return nil, mediaGap, parseErr
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].Start < spans[j].Start })
	for i := 1; i < len(spans); i++ {
		if spans[i].Start < spans[i-1].End {
			return nil, mediaGap, errors.New("overlapping opaque fields")
		}
	}
	return spans, mediaGap, nil
}

// Project returns the readable portion of one exact source range. Markers
// include the original offsets; no source data is deleted or overwritten.
func Project(data []byte, start, end int) (string, bool, error) {
	if start < 0 || end < start || end > len(data) {
		return "", false, errors.New("invalid source range")
	}
	spans, gap, err := UnreadableSpans(data)
	if err != nil {
		return "", gap, err
	}
	return projectSpans(data, start, end, spans), gap, nil
}

func projectSpans(data []byte, start, end int, spans []Span) string {
	var out bytes.Buffer
	pos := start
	for _, s := range spans {
		if s.End <= start || s.Start >= end {
			continue
		}
		left, right := max(start, s.Start), min(end, s.End)
		out.Write(data[pos:left])
		fmt.Fprintf(&out, "[%s不可直接读取，原文位置 %d–%d]", s.Kind, left, right)
		pos = right
	}
	out.Write(data[pos:end])
	return out.String()
}
