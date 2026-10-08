package health

import "time"

// SetSubscriptionGap shortens the spacing between background checks in tests.
func SetSubscriptionGap(d time.Duration) { subscriptionGap = d }
