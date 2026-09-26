package health

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"aiseat/internal/secure"
	"aiseat/internal/store"
)

// CheckGroup fetches the egress IP as seen through the group's proxy by GETting
// echoURL through that proxy and returning the trimmed response body.
//
// The proxy is applied via http.Transport.Proxy=http.ProxyURL(parsed), which
// handles http/https proxies. socks5 egress verification is best-effort: the
// stdlib http transport does not tunnel arbitrary TCP over socks5, so such a
// check may not be meaningfully exercised. An empty ProxyURL means a direct
// request (no proxy).
func CheckGroup(ctx context.Context, group store.IPGroup, echoURL string, timeout time.Duration) (ip string, err error) {
	tr := &http.Transport{}
	if strings.TrimSpace(group.ProxyURL) != "" {
		u, perr := url.Parse(group.ProxyURL)
		if perr != nil {
			return "", perr
		}
		tr.Proxy = http.ProxyURL(u)
	}
	client := &http.Client{Transport: tr, Timeout: timeout}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, echoURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

// CheckAll iterates all IP groups, checks each group's egress IP, records the
// result via RecordEgressCheck, and — when the observed IP differs from a
// group's non-empty ExpectedEgressIP with no error — invokes onChange so the
// caller can raise per-account "egress_changed" signals. onChange may be nil.
func CheckAll(ctx context.Context, st *store.Store, box *secure.Box, echoURL string, timeout time.Duration, onChange func(groupID int64, oldIP, newIP string)) error {
	groups, err := st.ListIPGroups(ctx, box)
	if err != nil {
		return err
	}
	for _, g := range groups {
		ip, cerr := CheckGroup(ctx, g, echoURL, timeout)
		errMsg := ""
		if cerr != nil {
			errMsg = cerr.Error()
		}
		if rerr := st.RecordEgressCheck(ctx, g.ID, ip, errMsg); rerr != nil {
			return rerr
		}
		if cerr == nil && g.ExpectedEgressIP != "" && ip != g.ExpectedEgressIP && onChange != nil {
			onChange(g.ID, g.ExpectedEgressIP, ip)
		}
	}
	return nil
}
