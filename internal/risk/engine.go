package risk

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"aiseat/internal/devicecard"
	"aiseat/internal/settings"
	"aiseat/internal/store"
)

// Rule names.
const (
	RuleNewDevice           = "new_device"
	RuleNonCodexBlocked     = "non_codex_blocked"
	RuleDatacenterIP        = "datacenter_ip"
	RuleTooManyDevices      = "too_many_devices"
	RuleConcurrentUntrusted = "concurrent_untrusted"
	RuleMultiNetwork        = "multi_network"
	RuleDeviceDenied        = "device_denied"
	RuleOutsideOffice       = "outside_office"
	RuleContentFlagged      = "content_flagged"
)

// multiNetworkMin is how many distinct coarse networks inside the concurrency
// window count as "the key is being used from several places at once".
const multiNetworkMin = 3

// Messenger delivers a text message to a Feishu open_id.
type Messenger interface {
	SendText(ctx context.Context, openID, text string) error
}

// CardMessenger also delivers interactive cards.
type CardMessenger interface {
	SendCard(ctx context.Context, openID string, card any) error
}

// Config wires an Engine.
type Config struct {
	Store     *store.Store
	Settings  *settings.Service
	Geo       GeoLookup // optional
	Messenger Messenger // optional; nil disables notifications
	PortalURL string    // public base URL of the portal
	// Cards signs the buttons of the new-device card. With it, and once Feishu
	// has verified the card callback URL, a new device is asked about with
	// 是我 / 不是我 buttons instead of a text message.
	Cards *devicecard.Signer
	Log   *slog.Logger
	Now   func() time.Time // for tests
}

// CachedDevice is the gate-relevant view of a device.
type CachedDevice struct {
	ID        int64
	Status    string
	FirstSeen time.Time
	Label     string
}

// Observation is what the data plane reports about one request.
type Observation struct {
	EmployeeID int64
	OpenID     string
	Device     CachedDevice // zero ID when the request was rejected before device lookup
	NewDevice  bool
	IP         string
	At         time.Time
	ClientKind string
	UserAgent  string
	Denied     string // gate error code when the request was rejected

	// Content audit result for the newest user message (empty rule = clean).
	ContentRule    string
	ContentSnippet string
	ContentBlocked bool
}

type cacheEntry struct {
	dev CachedDevice
	at  time.Time
}

// Engine registers devices for the data-plane gate and evaluates risk rules
// asynchronously so requests are never slowed by detection work.
type Engine struct {
	cfg    Config
	log    *slog.Logger
	shards []chan Observation
	wg     sync.WaitGroup
	win    *window

	devMu sync.Mutex
	devs  map[int64]map[string]cacheEntry

	seenMu sync.Mutex
	seen   map[string]time.Time

	// Uses are accumulated in memory and written every touchEvery instead of
	// one database write per request.
	touchMu  sync.Mutex
	devTouch map[int64]*store.Touch
	netTouch map[int64]*store.Touch
	netKnown map[netKeyT]int64
}

// deviceCacheTTL can be long: every device status change goes through
// DeviceChanged, which drops the cached entry immediately.
const deviceCacheTTL = 5 * time.Minute

// touchEvery is how often accumulated device/network uses are written.
const touchEvery = 30 * time.Second

type netKeyT struct {
	emp int64
	key string
}

// engineWorkers process observations in parallel (each costs a few DB round
// trips, which matter when the database is remote); one employee's
// observations always go to the same worker, in order.
const engineWorkers = 4

// NewEngine builds an Engine. Call Start to run the worker.
func NewEngine(cfg Config) *Engine {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	shards := make([]chan Observation, engineWorkers)
	for i := range shards {
		shards[i] = make(chan Observation, 4096/engineWorkers)
	}
	return &Engine{
		cfg: cfg, log: cfg.Log, shards: shards, win: newWindow(),
		devs: make(map[int64]map[string]cacheEntry), seen: make(map[string]time.Time),
		devTouch: map[int64]*store.Touch{}, netTouch: map[int64]*store.Touch{}, netKnown: map[netKeyT]int64{},
	}
}

// Start runs the observation workers until ctx is done.
func (e *Engine) Start(ctx context.Context) {
	for _, ch := range e.shards {
		go func(ch chan Observation) {
			for {
				select {
				case <-ctx.Done():
					return
				case o := <-ch:
					e.process(ctx, o)
					e.wg.Done()
				}
			}
		}(ch)
	}
	go func() {
		t := time.NewTicker(touchEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				e.flushTouches(fctx)
				cancel()
				return
			case <-t.C:
				e.flushTouches(ctx)
			}
		}
	}()
}

// Flush waits for queued observations to be processed and writes accumulated
// device/network uses (tests, shutdown).
func (e *Engine) Flush() {
	e.wg.Wait()
	e.flushTouches(context.Background())
}

func touch(m map[int64]*store.Touch, id int64, ip string, at time.Time) {
	t := m[id]
	if t == nil {
		t = &store.Touch{ID: id}
		m[id] = t
	}
	t.N++
	if !at.Before(t.At) {
		t.At, t.IP = at, ip
	}
}

func (e *Engine) flushTouches(ctx context.Context) {
	e.touchMu.Lock()
	devs := make([]store.Touch, 0, len(e.devTouch))
	for _, t := range e.devTouch {
		devs = append(devs, *t)
	}
	nets := make([]store.Touch, 0, len(e.netTouch))
	for _, t := range e.netTouch {
		nets = append(nets, *t)
	}
	e.devTouch, e.netTouch = map[int64]*store.Touch{}, map[int64]*store.Touch{}
	e.touchMu.Unlock()
	if err := e.cfg.Store.ApplyTouches(ctx, devs, nets); err != nil {
		e.log.Warn("risk: write device/network uses", "err", err)
		// Keep the counts for the next attempt.
		e.touchMu.Lock()
		for _, t := range devs {
			for i := int64(0); i < t.N; i++ {
				touch(e.devTouch, t.ID, t.IP, t.At)
			}
		}
		for _, t := range nets {
			for i := int64(0); i < t.N; i++ {
				touch(e.netTouch, t.ID, t.IP, t.At)
			}
		}
		e.touchMu.Unlock()
	}
}

// Observe queues an observation without blocking; it is dropped when the queue
// is full (detection is best-effort, serving requests is not).
func (e *Engine) Observe(o Observation) {
	e.wg.Add(1)
	select {
	case e.shards[uint64(o.EmployeeID)%uint64(len(e.shards))] <- o:
	default:
		e.wg.Done()
		e.log.Warn("risk: observation queue full, dropping", "employee", o.EmployeeID)
	}
}

// PortalDevicesURL is where employees confirm devices.
func (e *Engine) PortalDevicesURL() string {
	return strings.TrimRight(e.cfg.PortalURL, "/") + "/devices"
}

// Device returns the (cached) device for the request, registering it as
// pending on first sight. created is true when this call registered it.
func (e *Engine) Device(ctx context.Context, employeeID int64, info DeviceInfo, clientKind, ip string) (CachedDevice, bool, error) {
	now := e.cfg.Now()
	e.devMu.Lock()
	if ent, ok := e.devs[employeeID][info.Key]; ok && now.Sub(ent.at) < deviceCacheTTL {
		e.devMu.Unlock()
		return ent.dev, false, nil
	}
	e.devMu.Unlock()

	d, created, err := e.cfg.Store.EnsureDevice(ctx, store.Device{
		EmployeeID: employeeID, DeviceKey: info.Key, Kind: info.Kind, Label: info.Label,
		ClientKind: clientKind, UserAgent: info.UserAgent, FirstIP: ip,
	})
	if err != nil {
		return CachedDevice{}, false, err
	}
	cd := CachedDevice{ID: d.ID, Status: d.Status, FirstSeen: d.FirstSeen, Label: d.Label}
	// A device created trusted (the employee's first) is not news to anyone.
	created = created && d.Status != store.DeviceTrusted
	e.devMu.Lock()
	if e.devs[employeeID] == nil {
		e.devs[employeeID] = make(map[string]cacheEntry)
	}
	e.devs[employeeID][info.Key] = cacheEntry{dev: cd, at: now}
	e.devMu.Unlock()
	return cd, created, nil
}

// DeviceChanged drops cached state after a device's status changed so the
// gate sees it immediately.
func (e *Engine) DeviceChanged(employeeID, deviceID int64, status string) {
	e.devMu.Lock()
	delete(e.devs, employeeID)
	e.devMu.Unlock()
	e.win.setTrusted(employeeID, deviceID, status == store.DeviceTrusted)
}

// Unfrozen resets an employee's in-memory detection state after an admin
// unfreeze, so stale sightings do not immediately freeze them again.
func (e *Engine) Unfrozen(employeeID int64) {
	e.devMu.Lock()
	delete(e.devs, employeeID)
	e.devMu.Unlock()
	e.win.forget(employeeID)
	prefix := fmt.Sprintf("%d|", employeeID)
	e.seenMu.Lock()
	for k := range e.seen {
		if strings.HasPrefix(k, prefix) {
			delete(e.seen, k)
		}
	}
	e.seenMu.Unlock()
}

// once reports whether a (employee, rule, key) alert should fire now, muting
// repeats within span. With useDB it also consults stored events so restarts
// do not re-alert.
func (e *Engine) once(ctx context.Context, emp int64, rule, key string, span time.Duration, useDB bool) bool {
	now := e.cfg.Now()
	k := fmt.Sprintf("%d|%s|%s", emp, rule, key)
	e.seenMu.Lock()
	if t, ok := e.seen[k]; ok && now.Sub(t) < span {
		e.seenMu.Unlock()
		return false
	}
	e.seen[k] = now
	if len(e.seen) > 20000 {
		for kk, t := range e.seen {
			if now.Sub(t) > 48*time.Hour {
				delete(e.seen, kk)
			}
		}
	}
	e.seenMu.Unlock()
	if useDB {
		if has, err := e.cfg.Store.HasRecentRiskEvent(ctx, emp, rule, nil, now.Add(-span)); err == nil && has {
			return false
		}
	}
	return true
}

func (e *Engine) process(ctx context.Context, o Observation) {
	set := e.cfg.Settings.Get(ctx)
	now := o.At

	if o.ContentRule != "" {
		e.contentFlagged(ctx, o)
	}
	if o.Denied == "network_not_allowed" {
		e.outsideOffice(ctx, o)
		return
	}
	if o.Denied == "no_account_bound" {
		if e.once(ctx, o.EmployeeID, "no_account", "", 24*time.Hour, false) {
			e.broadcastAdmins(ctx, fmt.Sprintf("【AI 网关】%s 已经在用 Key，但还没有分配 AI 账号。请到「账号」页给其绑定一个：%s",
				e.employeeName(ctx, o.EmployeeID), strings.TrimRight(e.cfg.PortalURL, "/")+"/accounts"))
		}
		return
	}
	if o.Denied == "client_not_allowed" {
		if e.once(ctx, o.EmployeeID, RuleNonCodexBlocked, "", 24*time.Hour, true) {
			ev := e.addEvent(ctx, o.EmployeeID, RuleNonCodexBlocked, "warn", nil, nil,
				map[string]any{"user_agent": o.UserAgent, "ip": o.IP, "client_kind": o.ClientKind})
			e.notifyEmployee(ctx, o.OpenID, fmt.Sprintf(
				"【AI 网关】有一个非 Codex 客户端用你的 Key 发起了请求，已被拦截。\n客户端：%s\nIP：%s\n公司只允许用官方 Codex 客户端。如果不是你本人操作，说明 Key 可能已经泄露，请到门户重新生成：%s",
				short(o.UserAgent), o.IP, e.cfg.PortalURL))
			e.markNotified(ctx, ev)
		}
		return
	}
	if o.Device.ID == 0 {
		return
	}
	devID := o.Device.ID

	if o.NewDevice {
		ev := e.addEvent(ctx, o.EmployeeID, RuleNewDevice, "warn", &devID, nil,
			map[string]any{"label": o.Device.Label, "ip": o.IP, "location": e.location(o.IP)})
		hint := fmt.Sprintf("请在 %d 小时内确认，逾期会暂停这台设备。", set.DeviceConfirmHours)
		if o.Denied != "" {
			hint = "这台设备在确认前无法使用。"
		} else if set.RiskMode == settings.RiskLax {
			hint = "请及时确认。"
		}
		if !e.askWithCard(ctx, o, devID, hint) {
			e.notifyEmployee(ctx, o.OpenID, fmt.Sprintf(
				"【AI 网关】你的 Key 在一台新设备上被使用。\n设备：%s\nIP：%s %s\n如果是你本人，请到门户确认；如果不是你，请点「不是我」，Key 会立即作废。%s\n%s",
				o.Device.Label, o.IP, e.location(o.IP), hint, e.PortalDevicesURL()))
		}
		_ = e.cfg.Store.MarkDeviceNotified(ctx, devID)
		e.markNotified(ctx, ev)
		e.checkDeviceCount(ctx, o, set)
	}
	if o.Denied != "" {
		return
	}

	e.touchMu.Lock()
	touch(e.devTouch, devID, o.IP, now)
	e.touchMu.Unlock()

	key, coarse := NetKeys(o.IP)
	if key == "" {
		return
	}
	nk := netKeyT{o.EmployeeID, key}
	e.touchMu.Lock()
	netID, known := e.netKnown[nk]
	if known {
		touch(e.netTouch, netID, o.IP, now)
	}
	e.touchMu.Unlock()
	if !known {
		e.newNetwork(ctx, o, devID, key, nk, now)
	}
	e.windowCheck(ctx, o, set, devID, coarse, now)
}

// newNetwork registers a network not yet seen by this process (one write),
// alerting on a first use from a datacenter.
func (e *Engine) newNetwork(ctx context.Context, o Observation, devID int64, key string, nk netKeyT, now time.Time) {
	var geo GeoInfo
	if e.cfg.Geo != nil {
		geo, _ = e.cfg.Geo.Lookup(o.IP)
	}
	nw, newNet, err := e.cfg.Store.UpsertNetwork(ctx, store.Network{
		EmployeeID: o.EmployeeID, NetKey: key, IPSample: o.IP, Country: geo.Country,
		Province: geo.Province, City: geo.City, ISP: geo.ISP, Datacenter: geo.Datacenter,
	}, now)
	if err != nil {
		e.log.Warn("risk: upsert network", "err", err)
	} else if newNet && nw.Datacenter {
		nid := nw.ID
		ev := e.addEvent(ctx, o.EmployeeID, RuleDatacenterIP, "warn", &devID, &nid,
			map[string]any{"ip": o.IP, "isp": geo.ISP, "location": e.location(o.IP)})
		msg := fmt.Sprintf("Key 从机房/云服务器网络发起请求：%s（%s %s），设备：%s", o.IP, e.location(o.IP), geo.ISP, o.Device.Label)
		e.notifyEmployee(ctx, o.OpenID, "【AI 网关】你的 "+msg+"。如果是你挂了 VPN/代理，请改为直连；如果不是你，请到门户处理："+e.PortalDevicesURL())
		e.notifyAdmins(ctx, o.EmployeeID, msg)
		e.markNotified(ctx, ev)
	}
	if err == nil {
		e.touchMu.Lock()
		e.netKnown[nk] = nw.ID
		e.touchMu.Unlock()
	}
}

// windowCheck applies the concurrency rules for this sighting.
func (e *Engine) windowCheck(ctx context.Context, o Observation, set settings.Settings, devID int64, coarse string, now time.Time) {
	span := time.Duration(set.ConcurrencyWindowMinutes) * time.Minute
	grace := time.Duration(set.NewDeviceGraceMinutes) * time.Minute
	snap := e.win.add(o.EmployeeID, sighting{
		Device: devID, Net: coarse, At: now,
		Trusted: o.Device.Status == store.DeviceTrusted, FirstSeen: o.Device.FirstSeen,
	}, span)
	for _, f := range evalWindow(snap, now, grace, multiNetworkMin) {
		e.high(ctx, o, set, f)
	}
}

// outsideOffice records a key used outside the office networks and tells the
// employee (at most once a day).
func (e *Engine) outsideOffice(ctx context.Context, o Observation) {
	if !e.once(ctx, o.EmployeeID, RuleOutsideOffice, "", 24*time.Hour, true) {
		return
	}
	ev := e.addEvent(ctx, o.EmployeeID, RuleOutsideOffice, "warn", nil, nil,
		map[string]any{"ip": o.IP, "location": e.location(o.IP), "user_agent": o.UserAgent})
	e.notifyEmployee(ctx, o.OpenID, fmt.Sprintf(
		"【AI 网关】你的 Key 在公司网络以外被使用，已拦截。\nIP：%s %s\n如果是你本人在外办公，请联系管理员开通远程使用；如果不是你，请到门户检查设备，必要时重新生成 Key：%s",
		o.IP, e.location(o.IP), e.PortalDevicesURL()))
	e.markNotified(ctx, ev)
}

// contentFlagged records a content-audit hit and tells the administrators
// (the same employee and rule at most once an hour).
func (e *Engine) contentFlagged(ctx context.Context, o Observation) {
	if !e.once(ctx, o.EmployeeID, RuleContentFlagged, o.ContentRule, time.Hour, false) {
		return
	}
	var dev *int64
	if o.Device.ID != 0 {
		d := o.Device.ID
		dev = &d
	}
	ev := e.addEvent(ctx, o.EmployeeID, RuleContentFlagged, "warn", dev, nil,
		map[string]any{"rule": o.ContentRule, "snippet": o.ContentSnippet, "blocked": o.ContentBlocked, "ip": o.IP})
	action := "仅提醒，请求已放行"
	if o.ContentBlocked {
		action = "已拦截"
	}
	e.notifyAdmins(ctx, o.EmployeeID, fmt.Sprintf("触发内容审核规则「%s」（%s）：%s", o.ContentRule, action, o.ContentSnippet))
	e.markNotified(ctx, ev)
}

// high handles a high-severity window finding: record, freeze (unless lax) and notify.
func (e *Engine) high(ctx context.Context, o Observation, set settings.Settings, f finding) {
	if !e.once(ctx, o.EmployeeID, f.Rule, "", time.Hour, false) {
		return
	}
	var dev *int64
	if f.DeviceID != 0 {
		d := f.DeviceID
		dev = &d
	}
	ev := e.addEvent(ctx, o.EmployeeID, f.Rule, "high", dev, nil, f.Detail)
	reason := describeRule(f.Rule)
	if set.RiskMode == settings.RiskLax {
		e.notifyAdmins(ctx, o.EmployeeID, reason+"（宽松模式：仅通知，未冻结）"+detailText(f.Detail))
		e.markNotified(ctx, ev)
		return
	}
	froze, err := e.cfg.Store.FreezeEmployee(ctx, o.EmployeeID, reason)
	if err != nil {
		e.log.Warn("risk: freeze", "employee", o.EmployeeID, "err", err)
		return
	}
	if froze {
		e.notifyEmployee(ctx, o.OpenID, fmt.Sprintf(
			"【AI 网关】你的 Key 已被暂停使用。\n原因：%s。\n这通常说明 Key 可能被他人使用。请联系管理员核实后解冻。", reason))
		e.notifyAdmins(ctx, o.EmployeeID, reason+"，已自动冻结，请核实后在「风险中心」解冻。"+detailText(f.Detail))
	}
	e.markNotified(ctx, ev)
}

func (e *Engine) checkDeviceCount(ctx context.Context, o Observation, set settings.Settings) {
	n, err := e.cfg.Store.CountActiveDevices(ctx, o.EmployeeID, e.cfg.Now().Add(-7*24*time.Hour))
	if err != nil || n <= set.MaxActiveDevices {
		return
	}
	if !e.once(ctx, o.EmployeeID, RuleTooManyDevices, "", 24*time.Hour, true) {
		return
	}
	ev := e.addEvent(ctx, o.EmployeeID, RuleTooManyDevices, "warn", nil, nil,
		map[string]any{"active_devices": n, "limit": set.MaxActiveDevices})
	e.notifyAdmins(ctx, o.EmployeeID, fmt.Sprintf("7 天内在 %d 台设备上使用（上限 %d）", n, set.MaxActiveDevices))
	e.markNotified(ctx, ev)
}

// Notify sends an ad-hoc message to an employee (no-op without a messenger).
func (e *Engine) Notify(ctx context.Context, openID, text string) {
	e.notifyEmployee(ctx, openID, text)
}

// DeviceDenied handles an employee reporting "not me" for a device: the caller
// has already blocked the device and revoked the key.
func (e *Engine) DeviceDenied(ctx context.Context, emp store.Employee, dev store.Device) {
	id := dev.ID
	ev := e.addEvent(ctx, emp.ID, RuleDeviceDenied, "high", &id, nil,
		map[string]any{"label": dev.Label, "last_ip": dev.LastIP, "requests": dev.Requests})
	e.DeviceChanged(emp.ID, dev.ID, store.DeviceBlocked)
	e.notifyAdmins(ctx, emp.ID, fmt.Sprintf("员工报告设备「%s」不是本人（最近 IP %s，%d 次请求），已拉黑设备并作废 Key",
		dev.Label, dev.LastIP, dev.Requests))
	e.markNotified(ctx, ev)
}

// ExpirePending marks pending devices past the confirmation window as expired
// and tells their owners.
func (e *Engine) ExpirePending(ctx context.Context) error {
	set := e.cfg.Settings.Get(ctx)
	if set.RiskMode == settings.RiskLax {
		return nil
	}
	cutoff := e.cfg.Now().Add(-time.Duration(set.DeviceConfirmHours) * time.Hour)
	devs, err := e.cfg.Store.ExpirePendingDevices(ctx, cutoff)
	if err != nil {
		return err
	}
	for _, d := range devs {
		e.DeviceChanged(d.EmployeeID, d.ID, store.DeviceExpired)
		emp, err := e.cfg.Store.GetEmployee(ctx, d.EmployeeID)
		if err != nil {
			continue
		}
		e.notifyEmployee(ctx, emp.FeishuOpenID, fmt.Sprintf(
			"【AI 网关】设备「%s」超过 %d 小时没有确认，已暂停使用。如果是你本人的设备，确认后即可恢复：%s",
			d.Label, set.DeviceConfirmHours, e.PortalDevicesURL()))
	}
	return nil
}

// --- helpers ---

func (e *Engine) addEvent(ctx context.Context, emp int64, rule, sev string, dev, nw *int64, detail map[string]any) store.RiskEvent {
	ev, err := e.cfg.Store.AddRiskEvent(ctx, store.RiskEvent{
		EmployeeID: emp, Rule: rule, Severity: sev, DeviceID: dev, NetworkID: nw, Detail: detail,
	})
	if err != nil {
		e.log.Warn("risk: add event", "rule", rule, "employee", emp, "err", err)
	}
	return ev
}

func (e *Engine) markNotified(ctx context.Context, ev store.RiskEvent) {
	if ev.ID != 0 && e.cfg.Messenger != nil {
		_ = e.cfg.Store.MarkRiskEventNotified(ctx, ev.ID)
	}
}

// askWithCard asks about a new device with a Feishu card, when cards can be
// used. It reports whether the card went out.
func (e *Engine) askWithCard(ctx context.Context, o Observation, deviceID int64, hint string) bool {
	cm, ok := e.cfg.Messenger.(CardMessenger)
	if !ok || e.cfg.Cards == nil || o.OpenID == "" || !e.cfg.Store.CardCallbackReady(ctx) {
		return false
	}
	now := e.cfg.Now()
	d := devicecard.Device{Label: o.Device.Label, IP: o.IP, Location: e.location(o.IP), Hint: hint, DevicesURL: e.PortalDevicesURL()}
	card := devicecard.Ask(d, e.cfg.Cards.Token(devicecard.Confirm, o.EmployeeID, deviceID, now),
		e.cfg.Cards.Token(devicecard.Deny, o.EmployeeID, deviceID, now))
	if err := cm.SendCard(ctx, o.OpenID, card); err != nil {
		e.log.Warn("risk: new-device card", "err", err)
		return false
	}
	return true
}

func (e *Engine) notifyEmployee(ctx context.Context, openID, text string) {
	if e.cfg.Messenger == nil || openID == "" {
		return
	}
	if err := e.cfg.Messenger.SendText(ctx, openID, text); err != nil {
		e.log.Warn("risk: notify employee", "err", err)
	}
}

func (e *Engine) notifyAdmins(ctx context.Context, emp int64, what string) {
	e.broadcastAdmins(ctx, fmt.Sprintf("【AI 网关·风险】%s：%s", e.employeeName(ctx, emp), what))
}

func (e *Engine) broadcastAdmins(ctx context.Context, text string) {
	if e.cfg.Messenger == nil {
		return
	}
	ids, err := e.cfg.Store.SuperAdminOpenIDs(ctx)
	if err != nil {
		return
	}
	for _, id := range ids {
		if err := e.cfg.Messenger.SendText(ctx, id, text); err != nil {
			e.log.Warn("risk: notify admin", "err", err)
		}
	}
}

func (e *Engine) employeeName(ctx context.Context, id int64) string {
	if emp, err := e.cfg.Store.GetEmployee(ctx, id); err == nil && emp.Name != "" {
		return emp.Name
	}
	return fmt.Sprintf("员工#%d", id)
}

func (e *Engine) location(ip string) string {
	if e.cfg.Geo == nil {
		return ""
	}
	g, ok := e.cfg.Geo.Lookup(ip)
	if !ok {
		return ""
	}
	parts := []string{}
	for _, p := range []string{g.Country, g.Province, g.City} {
		if p != "" && (len(parts) == 0 || parts[len(parts)-1] != p) {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, " ")
}

func describeRule(rule string) string {
	switch rule {
	case RuleConcurrentUntrusted:
		return "未确认的设备与另一台设备在不同网络同时使用 Key"
	case RuleMultiNetwork:
		return "短时间内在 3 个以上不同网络同时使用 Key"
	case RuleDeviceDenied:
		return "员工报告有设备不是本人"
	default:
		return rule
	}
}

func detailText(d map[string]any) string {
	if len(d) == 0 {
		return ""
	}
	return fmt.Sprintf("\n详情：%v", d)
}

func short(s string) string {
	if len(s) > 120 {
		return s[:120] + "…"
	}
	if s == "" {
		return "（无 User-Agent）"
	}
	return s
}
