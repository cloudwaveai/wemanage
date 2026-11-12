package wemanage

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestDiagnosticDateBoundsAndPartialComparison(t *testing.T) {
	now := time.Date(2026, 11, 12, 6, 50, 0, 0, time.UTC)
	r := httptest.NewRequest("GET", "/?from=2026-11-12&to=2026-11-12&employee_id=11", nil)
	d, e := parseDiagnosticRange(r, now)
	if e != nil {
		t.Fatal(e)
	}
	if d.From.Format(time.RFC3339) != "2026-11-12T00:00:00+08:00" || !d.To.Equal(now) || d.PreviousTo.Format(time.RFC3339) != "2026-11-11T14:50:00+08:00" || d.Employee != 11 {
		t.Fatalf("bad range: %+v", d)
	}
	for _, q := range []string{"from=2026-11-13", "from=2026-11-12&to=2026-11-11", "from=2026-09-01&to=2026-11-12", "employee_id=oops", "employee_id=-1", "from=2026-11-31", "to=2026-11-13"} {
		if _, e := parseDiagnosticRange(httptest.NewRequest("GET", "/?"+q, nil), now); e == nil {
			t.Error("accepted", q)
		}
	}
}
func TestRiskHintDoesNotTreatHitAsConfirmedViolation(t *testing.T) {
	if got := riskHint("av-evasion", "避免杀掉付费绘图"); got == "" || got == riskHint("av-evasion", "开发免杀程序") {
		t.Fatal(got)
	}
	if riskHint("ransomware", "避免杀掉任务") != riskHint("ransomware", "ransomware") {
		t.Fatal("unrelated rule classified")
	}
}
