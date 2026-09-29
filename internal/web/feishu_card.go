package web

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aiseat/internal/devicecard"
	"aiseat/internal/store"

	"github.com/tidwall/gjson"
)

// handleFeishuCard receives Feishu's card callbacks: the URL check Feishu
// makes when the callback address is saved in the developer console, and the
// 是我 / 不是我 buttons of the new-device card. Feishu wants an answer
// within three seconds; the answer's toast and card replace the one clicked.
//
// Anyone can post here. What makes a click count is the sealed token on the
// button (see devicecard), plus the Feishu Verification Token and Encrypt
// Key when they are configured.
func (s *Server) handleFeishuCard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	if enc := gjson.GetBytes(raw, "encrypt").String(); enc != "" {
		key := s.deps.FeishuEncryptKey
		if key == "" {
			s.deps.Log.Warn("feishu card: encrypted callback, but FEISHU_ENCRYPT_KEY is not set")
			writeErr(w, http.StatusBadRequest, "encrypted callbacks need FEISHU_ENCRYPT_KEY")
			return
		}
		if !feishuSignatureOK(r.Header, raw, key) {
			writeErr(w, http.StatusUnauthorized, "bad signature")
			return
		}
		if raw, err = feishuDecrypt(enc, key); err != nil {
			writeErr(w, http.StatusBadRequest, "cannot decrypt")
			return
		}
	}
	if want := s.deps.FeishuVerifyToken; want != "" {
		got := gjson.GetBytes(raw, "header.token").String()
		if got == "" {
			got = gjson.GetBytes(raw, "token").String()
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			writeErr(w, http.StatusUnauthorized, "bad verification token")
			return
		}
	}

	if gjson.GetBytes(raw, "type").String() == "url_verification" {
		// Feishu can now reach us: new devices are asked about with cards.
		if err := s.deps.Store.MarkCardCallbackReady(ctx); err != nil {
			s.deps.Log.Warn("feishu card: mark ready", "err", err)
		}
		writeJSON(w, http.StatusOK, map[string]any{"challenge": gjson.GetBytes(raw, "challenge").String()})
		return
	}

	reply := func(kind, text string, card map[string]any) {
		out := map[string]any{"toast": map[string]any{"type": kind, "content": text}}
		if card != nil {
			out["card"] = map[string]any{"type": "raw", "data": card}
		}
		writeJSON(w, http.StatusOK, out)
	}
	// card.action.trigger (schema 2.0); the flat fields are the older shape.
	tok := firstNonEmpty(gjson.GetBytes(raw, "event.action.value.t").String(), gjson.GetBytes(raw, "action.value.t").String())
	operator := firstNonEmpty(gjson.GetBytes(raw, "event.operator.open_id").String(), gjson.GetBytes(raw, "open_id").String())
	devicesURL := strings.TrimRight(s.deps.PublicBaseURL, "/") + "/devices"
	if s.deps.Cards == nil || tok == "" {
		reply("error", "这个按钮用不了了，请到门户「我的设备」处理。", nil)
		return
	}
	claim, err := s.deps.Cards.Parse(tok, time.Now())
	if err != nil {
		reply("error", "这个按钮已过期，请到门户「我的设备」处理。", nil)
		return
	}
	emp, err := s.deps.Store.GetEmployee(ctx, claim.EmployeeID)
	if err != nil || (operator != "" && operator != emp.FeishuOpenID) {
		reply("error", "只能由本人操作。", nil)
		return
	}
	d, err := s.deps.Store.GetDevice(ctx, claim.DeviceID)
	if err != nil || d.EmployeeID != emp.ID {
		reply("error", "找不到这台电脑，请到门户「我的设备」查看。", nil)
		return
	}
	view := devicecard.Device{Label: d.Label, IP: d.LastIP, DevicesURL: devicesURL}
	actor := &emp.ID
	detail := map[string]any{"label": d.Label, "last_ip": d.LastIP, "via": "feishu_card"}

	switch claim.Action {
	case devicecard.Confirm:
		if d.Status == store.DeviceBlocked {
			reply("info", "这台电脑已被停用", devicecard.Done(view, "这台电脑已被停用", "red",
				"如需恢复，请联系管理员。"))
			return
		}
		was := d.Status
		if code, msg := s.confirmDevice(ctx, emp, d); code != http.StatusOK {
			reply("error", msg, nil)
			return
		}
		if was != store.DeviceTrusted {
			s.auditAs(ctx, actor, "", "device_confirm", "device", fmt.Sprint(d.ID), detail)
		}
		reply("success", "已确认", devicecard.Done(view, "已确认是你", "green", "这台电脑可以正常使用了。"))
	case devicecard.Deny:
		done := devicecard.Done(view, "已停用这台电脑", "red",
			fmt.Sprintf("你的 Key 已作废，请到门户重新生成：[我的 Key](%s/key)", strings.TrimRight(s.deps.PublicBaseURL, "/")))
		if d.Status == store.DeviceBlocked {
			reply("info", "这台电脑之前已经停用了", done)
			return
		}
		if code, msg := s.denyDevice(ctx, emp, d); code != http.StatusOK {
			reply("error", msg, nil)
			return
		}
		detail["key_revoked"] = true
		s.auditAs(ctx, actor, "", "device_deny", "device", fmt.Sprint(d.ID), detail)
		reply("success", "已停用，Key 已作废", done)
	}
}

// feishuSignatureOK checks X-Lark-Signature = sha256(timestamp + nonce +
// encrypt key + body), sent with encrypted callbacks.
func feishuSignatureOK(h http.Header, body []byte, key string) bool {
	sig := h.Get("X-Lark-Signature")
	if sig == "" {
		return true // older callbacks are not signed; the decryption must still succeed
	}
	sum := sha256.Sum256([]byte(h.Get("X-Lark-Request-Timestamp") + h.Get("X-Lark-Request-Nonce") + key + string(body)))
	return subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(sig)) == 1
}

// feishuDecrypt opens an encrypted callback: AES-256-CBC keyed with
// sha256(encrypt key), the IV in front, PKCS#7 padding.
func feishuDecrypt(enc, key string) ([]byte, error) {
	buf, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		return nil, err
	}
	if len(buf) < 2*aes.BlockSize || len(buf)%aes.BlockSize != 0 {
		return nil, errors.New("feishu: bad ciphertext length")
	}
	k := sha256.Sum256([]byte(key))
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, err
	}
	iv, data := buf[:aes.BlockSize], buf[aes.BlockSize:]
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(data, data)
	n := int(data[len(data)-1])
	if n < 1 || n > aes.BlockSize {
		return nil, errors.New("feishu: bad padding")
	}
	return data[:len(data)-n], nil
}
