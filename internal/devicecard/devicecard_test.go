package devicecard

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"aiseat/internal/secure"
)

func TestTokens(t *testing.T) {
	box, _ := secure.NewBox(make([]byte, 32))
	s := Signer{Box: box}
	now := time.Now()
	tok := s.Token(Confirm, 7, 42, now)
	c, err := s.Parse(tok, now)
	if err != nil || c.Action != Confirm || c.EmployeeID != 7 || c.DeviceID != 42 {
		t.Fatalf("round trip: %+v %v", c, err)
	}
	if _, err := s.Parse(tok, now.Add(8*24*time.Hour)); err == nil {
		t.Fatal("expired token accepted")
	}
	other, _ := secure.NewBox([]byte(strings.Repeat("k", 32)))
	if _, err := (Signer{Box: other}).Parse(tok, now); err == nil {
		t.Fatal("token from another key accepted")
	}
	if _, err := s.Parse(tok[:len(tok)-2]+"AA", now); err == nil {
		t.Fatal("tampered token accepted")
	}
}

func TestAskCardShape(t *testing.T) {
	c := Ask(Device{Label: "MacBook", IP: "1.2.3.4", Location: "北京", Hint: "24 小时内确认。", DevicesURL: "https://p/devices"}, "ct", "dt")
	b, _ := json.Marshal(c)
	s := string(b)
	for _, want := range []string{`"schema":"2.0"`, `"t":"ct"`, `"t":"dt"`, `"confirm":`, "MacBook", "1.2.3.4（北京）", "https://p/devices"} {
		if !strings.Contains(s, want) {
			t.Errorf("card lacks %s: %s", want, s)
		}
	}
}
