package workrecords

import (
	"bytes"
	"errors"

	"github.com/tidwall/gjson"
)

// reasoningCipherRange identifies only the root encrypted_content field of a
// structured reasoning item. Every original byte remains in the snapshot; the
// model receives a bounded reference because ciphertext is not interpretable.
var ErrOpaqueReasoning = errors.New("encrypted reasoning requires an opaque reference")

func reasoningCipherRange(data []byte) (int, int, bool, error) {
	if !gjson.ValidBytes(data) {
		if bytes.Contains(data, []byte(`"encrypted_content"`)) {
			return 0, 0, false, errors.New("invalid encrypted reasoning structure")
		}
		return 0, 0, false, nil
	}
	if gjson.GetBytes(data, "type").String() != "reasoning" {
		return 0, 0, false, nil
	}
	field := gjson.GetBytes(data, "encrypted_content")
	if !field.Exists() {
		return 0, 0, false, nil
	}
	if field.Type != gjson.String {
		return 0, 0, false, errors.New("unsupported encrypted field type")
	}
	start, end := field.Index, field.Index+len(field.Raw)
	if start < 0 || end > len(data) || !bytes.Equal(data[start:end], []byte(field.Raw)) {
		return 0, 0, false, errors.New("invalid encrypted field range")
	}
	return start, end, true, nil
}

const opaqueReasoningText = "[加密推理字段无法直接读取；原始字节及位置已保留]"
