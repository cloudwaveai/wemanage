package store_test

import (
	"testing"
	"time"

	"aiseat/internal/store"
)

func TestAdminBriefingPrefs(t *testing.T) {
	s, ctx := newStore(t)
	e := mkEmployee(t, s, ctx, "ou_admin1", "管理员一")

	// No row yet: the defaults apply (enabled, no custom prompt).
	pref, found, err := s.GetAdminBriefingPref(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if found || !pref.Enabled || pref.Prompt != "" {
		t.Fatalf("defaults = %+v found=%v", pref, found)
	}

	if err := s.PutAdminBriefingPref(ctx, e.ID, false, "只关心风险"); err != nil {
		t.Fatal(err)
	}
	pref, found, err = s.GetAdminBriefingPref(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !found || pref.Enabled || pref.Prompt != "只关心风险" {
		t.Fatalf("saved = %+v found=%v", pref, found)
	}

	// Replace.
	if err := s.PutAdminBriefingPref(ctx, e.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	pref, _, _ = s.GetAdminBriefingPref(ctx, e.ID)
	if !pref.Enabled || pref.Prompt != "" {
		t.Fatalf("replaced = %+v", pref)
	}

	// The employee disappears -> the pref row goes with them.
	if err := s.SetEmployeeStatus(ctx, e.ID, "disabled"); err != nil {
		t.Fatal(err)
	}
}

func TestAdminBriefingRows(t *testing.T) {
	s, ctx := newStore(t)
	e := mkEmployee(t, s, ctx, "ou_admin2", "管理员二")
	week := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)

	if _, err := s.GetAdminBriefing(ctx, e.ID, week, week.AddDate(0, 0, 4)); err != store.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := s.StartAdminBriefing(ctx, e.ID, week, week.AddDate(0, 0, 4)); err != nil {
		t.Fatal(err)
	}
	row, err := s.GetAdminBriefing(ctx, e.ID, week, week.AddDate(0, 0, 4))
	if err != nil || row.Status != "running" || row.ContentMD != "" {
		t.Fatalf("started = %+v %v", row, err)
	}
	// A rerun clears the previous output.
	if err := s.FinishAdminBriefing(ctx, e.ID, week, week.AddDate(0, 0, 4), "第一版", "gpt-6-luna", "test", "", false); err != nil {
		t.Fatal(err)
	}
	if err := s.StartAdminBriefing(ctx, e.ID, week, week.AddDate(0, 0, 4)); err != nil {
		t.Fatal(err)
	}
	row, _ = s.GetAdminBriefing(ctx, e.ID, week, week.AddDate(0, 0, 4))
	if row.Status != "running" || row.ContentMD != "" || row.GeneratedAt != nil {
		t.Fatalf("rerun did not reset: %+v", row)
	}

	// Failed runs keep no content; sent runs carry the text and the time.
	if err := s.FinishAdminBriefing(ctx, e.ID, week, week.AddDate(0, 0, 4), "", "", "failed", "boom", false); err != nil {
		t.Fatal(err)
	}
	row, _ = s.GetAdminBriefing(ctx, e.ID, week, week.AddDate(0, 0, 4))
	if row.Status != "failed" || row.Error != "boom" {
		t.Fatalf("failed = %+v", row)
	}
	if err := s.FinishAdminBriefing(ctx, e.ID, week, week.AddDate(0, 0, 4), "最终版", "gpt-6-luna", "sent", "", true); err != nil {
		t.Fatal(err)
	}
	row, _ = s.GetAdminBriefing(ctx, e.ID, week, week.AddDate(0, 0, 4))
	if row.Status != "sent" || row.ContentMD != "最终版" || row.SentAt == nil {
		t.Fatalf("sent = %+v", row)
	}

	// History is most recently generated first.
	older := week.AddDate(0, 0, -7)
	if err := s.StartAdminBriefing(ctx, e.ID, older, older.AddDate(0, 0, 4)); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAdminBriefing(ctx, e.ID, older, older.AddDate(0, 0, 4), "旧的一周", "gpt-6-luna", "sent", "", true); err != nil {
		t.Fatal(err)
	}
	hist, err := s.RecentAdminBriefings(ctx, e.ID, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 || hist[0].WeekStart != older || hist[1].WeekStart != week {
		t.Fatalf("history = %+v", hist)
	}

	// Retention drops old weeks, keeps prefs.
	cutoff := week.AddDate(0, 0, 1)
	if err := s.DeleteExpiredAdminBriefings(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAdminBriefing(ctx, e.ID, week, week.AddDate(0, 0, 4)); err != store.ErrNotFound {
		t.Fatalf("week row should be gone: %v", err)
	}
	if _, err := s.GetAdminBriefing(ctx, e.ID, older, older.AddDate(0, 0, 4)); err != store.ErrNotFound {
		t.Fatalf("older row should be gone: %v", err)
	}
}

func TestListSuperAdminsAndDigestDays(t *testing.T) {
	s, ctx := newStore(t)
	admin := mkEmployee(t, s, ctx, "ou_root", "根管理员")
	plain := mkEmployee(t, s, ctx, "ou_peon", "普通员工")
	if err := s.SetSuperAdmin(ctx, admin.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSuperAdmin(ctx, plain.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEmployeeStatus(ctx, plain.ID, "disabled"); err != nil {
		t.Fatal(err)
	}

	admins, err := s.ListSuperAdmins(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(admins) != 1 || admins[0].ID != admin.ID {
		t.Fatalf("admins = %+v", admins)
	}

	// Days in range returns runs oldest first, in-range only.
	d1 := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	d2 := d1.AddDate(0, 0, 1)
	out := d1.AddDate(0, 0, 3)
	for _, d := range []time.Time{d1, d2} {
		if err := s.StartWorkDigestDay(ctx, d); err != nil {
			t.Fatal(err)
		}
		if err := s.FinishWorkDigestDay(ctx, d, "概览", "gpt-6-luna", "done", ""); err != nil {
			t.Fatal(err)
		}
	}
	days, err := s.WorkDigestDaysInRange(ctx, d1, out)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 || !days[0].Day.Equal(d1) || !days[1].Day.Equal(d2) {
		t.Fatalf("days = %+v", days)
	}
	if days, err := s.WorkDigestDaysInRange(ctx, d2, out); err != nil || len(days) != 1 {
		t.Fatalf("subrange = %+v %v", days, err)
	}
}

func TestAdminBriefingRanges(t *testing.T) {
	s, ctx := newStore(t)
	e := mkEmployee(t, s, ctx, "ou_ranges", "管理员")
	from := time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)
	short, long := from.AddDate(0, 0, 1), from.AddDate(0, 0, 3)
	for _, end := range []time.Time{short, long} {
		if err := s.StartAdminBriefing(ctx, e.ID, from, end); err != nil {
			t.Fatal(err)
		}
		if err := s.FinishAdminBriefing(ctx, e.ID, from, end, end.Format(time.DateOnly), "m", "sent", "", true); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.StartAdminBriefing(ctx, e.ID, from, short); err != nil {
		t.Fatal(err)
	}
	row, err := s.GetAdminBriefing(ctx, e.ID, from, short)
	if err != nil || row.Status != "running" || row.SentAt != nil || row.Model != "" {
		t.Fatalf("rerun = %+v %v", row, err)
	}
	row, err = s.GetAdminBriefing(ctx, e.ID, from, long)
	if err != nil || row.Status != "sent" || row.ContentMD != long.Format(time.DateOnly) {
		t.Fatalf("other range changed: %+v %v", row, err)
	}
	hist, err := s.RecentAdminBriefings(ctx, e.ID, 20)
	if err != nil || len(hist) != 2 {
		t.Fatalf("history = %+v %v", hist, err)
	}
}
