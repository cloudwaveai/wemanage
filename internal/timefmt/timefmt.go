// Package timefmt renders times the way the portal and messages speak them.
package timefmt

import (
	"fmt"
	"time"
)

// Relative renders t for someone at now: "15:20", "明天 09:10" or
// "10月3日 09:10", in loc (the local zone when nil).
func Relative(t, now time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	t, now = t.In(loc), now.In(loc)
	day := func(x time.Time) time.Time { return time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, loc) }
	switch day(t).Sub(day(now)) {
	case 0:
		return t.Format("15:04")
	case 24 * time.Hour:
		return "明天 " + t.Format("15:04")
	}
	return fmt.Sprintf("%d月%d日 %s", t.Month(), t.Day(), t.Format("15:04"))
}
