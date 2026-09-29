// Package devicecard builds the Feishu card that asks an employee whether a
// new device is theirs, and reads its button presses back.
//
// Each button carries a token sealed with the gateway's secret box, naming
// the action, the employee and the device. Feishu hands the token back on a
// click; only the gateway can have made it, so a forged callback cannot
// confirm someone else's device.
package devicecard

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"aiseat/internal/secure"
)

const (
	Confirm = "confirm"
	Deny    = "deny"

	tokenTTL = 7 * 24 * time.Hour
)

// Claim is what a button's token says.
type Claim struct {
	Action     string `json:"a"`
	EmployeeID int64  `json:"e"`
	DeviceID   int64  `json:"d"`
	Expires    int64  `json:"x"`
}

// Signer makes and reads button tokens.
type Signer struct{ Box *secure.Box }

func (s Signer) Token(action string, employeeID, deviceID int64, now time.Time) string {
	b, _ := json.Marshal(Claim{Action: action, EmployeeID: employeeID, DeviceID: deviceID, Expires: now.Add(tokenTTL).Unix()})
	return base64.RawURLEncoding.EncodeToString(s.Box.Seal(b))
}

var ErrBadToken = errors.New("devicecard: bad or expired token")

func (s Signer) Parse(tok string, now time.Time) (Claim, error) {
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return Claim{}, ErrBadToken
	}
	plain, err := s.Box.Open(raw)
	if err != nil {
		return Claim{}, ErrBadToken
	}
	var c Claim
	if json.Unmarshal(plain, &c) != nil || now.Unix() > c.Expires || (c.Action != Confirm && c.Action != Deny) {
		return Claim{}, ErrBadToken
	}
	return c, nil
}

// Device is what the card shows about the device.
type Device struct {
	Label, IP, Location string
	Hint                string // e.g. how long it works before it must be confirmed
	DevicesURL          string // the portal's "my devices" page
}

// Ask is the card for a new device, with 是我 / 不是我 buttons.
func Ask(d Device, confirmToken, denyToken string) map[string]any {
	where := d.IP
	if d.Location != "" {
		where += "（" + d.Location + "）"
	}
	button := func(text, typ, token string, confirm map[string]any) map[string]any {
		b := map[string]any{
			"tag": "button", "type": typ,
			"text":      plain(text),
			"behaviors": []any{map[string]any{"type": "callback", "value": map[string]any{"t": token}}},
		}
		if confirm != nil {
			b["confirm"] = confirm
		}
		return b
	}
	return card("你的 Key 在一台新电脑上使用", "blue",
		markdown(fmt.Sprintf("**电脑**：%s\n**IP**：%s\n\n是你本人吗？", d.Label, where)),
		map[string]any{
			"tag": "column_set", "horizontal_spacing": "8px",
			"columns": []any{
				column(button("是我", "primary_filled", confirmToken, nil)),
				column(button("不是我", "danger", denyToken, map[string]any{
					"title": plain("确认不是你？"),
					"text":  plain("这台电脑会被停用，你的 Key 会立即作废，之后需要重新生成。"),
				})),
			},
		},
		markdown(fmt.Sprintf("<font color='grey'>%s也可以在门户处理：[我的设备](%s)</font>", d.Hint, d.DevicesURL)),
	)
}

// Done is the card after the question is settled: no buttons, just the outcome.
func Done(d Device, title, template, text string) map[string]any {
	return card(title, template,
		markdown(fmt.Sprintf("**电脑**：%s\n**IP**：%s\n\n%s", d.Label, d.IP, text)))
}

func card(title, template string, elements ...any) map[string]any {
	return map[string]any{
		"schema": "2.0",
		"config": map[string]any{"update_multi": true},
		"header": map[string]any{"title": plain(title), "template": template},
		"body":   map[string]any{"elements": elements},
	}
}

func plain(s string) map[string]any { return map[string]any{"tag": "plain_text", "content": s} }

func markdown(s string) map[string]any { return map[string]any{"tag": "markdown", "content": s} }

func column(el any) map[string]any {
	return map[string]any{"tag": "column", "width": "auto", "elements": []any{el}}
}
