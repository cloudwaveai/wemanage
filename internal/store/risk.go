package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// Device statuses.
const (
	DevicePending = "pending"
	DeviceTrusted = "trusted"
	DeviceBlocked = "blocked"
	DeviceExpired = "expired"
)

// Device is a client device (a Codex installation, or a UA fingerprint) that
// an employee's key has been used from.
type Device struct {
	ID          int64      `json:"id"`
	EmployeeID  int64      `json:"employee_id"`
	DeviceKey   string     `json:"device_key"`
	Kind        string     `json:"kind"`
	Label       string     `json:"label"`
	ClientKind  string     `json:"client_kind"`
	UserAgent   string     `json:"user_agent"`
	Status      string     `json:"status"`
	FirstSeen   time.Time  `json:"first_seen"`
	LastSeen    time.Time  `json:"last_seen"`
	FirstIP     string     `json:"first_ip"`
	LastIP      string     `json:"last_ip"`
	Requests    int64      `json:"requests"`
	ConfirmedAt *time.Time `json:"confirmed_at"`
	NotifiedAt  *time.Time `json:"notified_at"`
}

// Network is a client network (/24 or /48) an employee's key was used from.
type Network struct {
	ID         int64     `json:"id"`
	EmployeeID int64     `json:"employee_id"`
	NetKey     string    `json:"net_key"`
	IPSample   string    `json:"ip_sample"`
	Country    string    `json:"country"`
	Province   string    `json:"province"`
	City       string    `json:"city"`
	ISP        string    `json:"isp"`
	Datacenter bool      `json:"datacenter"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	Requests   int64     `json:"requests"`
}

// RiskEvent is a detected risk.
type RiskEvent struct {
	ID           int64          `json:"id"`
	EmployeeID   int64          `json:"employee_id"`
	EmployeeName string         `json:"employee_name"`
	Rule         string         `json:"rule"`
	Severity     string         `json:"severity"`
	Detail       map[string]any `json:"detail"`
	DeviceID     *int64         `json:"device_id"`
	NetworkID    *int64         `json:"network_id"`
	At           time.Time      `json:"at"`
	NotifiedAt   *time.Time     `json:"notified_at"`
	ResolvedAt   *time.Time     `json:"resolved_at"`
}

const deviceCols = `id, employee_id, device_key, kind, label, client_kind, user_agent, status,
	first_seen, last_seen, first_ip, last_ip, requests, confirmed_at, notified_at`

func scanDevice(r pgx.Row) (Device, error) {
	var d Device
	err := r.Scan(&d.ID, &d.EmployeeID, &d.DeviceKey, &d.Kind, &d.Label, &d.ClientKind, &d.UserAgent, &d.Status,
		&d.FirstSeen, &d.LastSeen, &d.FirstIP, &d.LastIP, &d.Requests, &d.ConfirmedAt, &d.NotifiedAt)
	return d, err
}

// EnsureDevice returns the device for (employee, key), inserting it as pending
// when it is new. created reports whether this call inserted it.
func (s *Store) EnsureDevice(ctx context.Context, d Device) (dev Device, created bool, err error) {
	var inserted bool
	row := s.Pool.QueryRow(ctx, `
		INSERT INTO client_devices (employee_id, device_key, kind, label, client_kind, user_agent, status, first_ip, last_ip)
		VALUES ($1,$2,$3,$4,$5,$6,'pending',$7,$7)
		ON CONFLICT (employee_id, device_key) DO UPDATE SET device_key = EXCLUDED.device_key
		RETURNING `+deviceCols+`, (xmax = 0)`,
		d.EmployeeID, d.DeviceKey, d.Kind, d.Label, d.ClientKind, d.UserAgent, d.FirstIP)
	err = row.Scan(&dev.ID, &dev.EmployeeID, &dev.DeviceKey, &dev.Kind, &dev.Label, &dev.ClientKind, &dev.UserAgent, &dev.Status,
		&dev.FirstSeen, &dev.LastSeen, &dev.FirstIP, &dev.LastIP, &dev.Requests, &dev.ConfirmedAt, &dev.NotifiedAt, &inserted)
	return dev, inserted, err
}

// Touch is accumulated use of a device or network since the last flush.
type Touch struct {
	ID int64
	N  int64
	At time.Time // latest use
	IP string    // IP of the latest use
}

// ApplyTouches adds accumulated uses to devices and networks in one round trip.
func (s *Store) ApplyTouches(ctx context.Context, devices, networks []Touch) error {
	if len(devices) == 0 && len(networks) == 0 {
		return nil
	}
	cols := func(ts []Touch) ([]int64, []int64, []time.Time, []string) {
		ids, ns, ats, ips := make([]int64, len(ts)), make([]int64, len(ts)), make([]time.Time, len(ts)), make([]string, len(ts))
		for i, t := range ts {
			ids[i], ns[i], ats[i], ips[i] = t.ID, t.N, t.At, t.IP
		}
		return ids, ns, ats, ips
	}
	batch := &pgx.Batch{}
	if len(devices) > 0 {
		ids, ns, ats, ips := cols(devices)
		batch.Queue(`
			UPDATE client_devices d SET last_seen = GREATEST(d.last_seen, v.at), last_ip = v.ip, requests = d.requests + v.n
			FROM unnest($1::bigint[], $2::bigint[], $3::timestamptz[], $4::text[]) AS v(id, n, at, ip)
			WHERE d.id = v.id`, ids, ns, ats, ips)
	}
	if len(networks) > 0 {
		ids, ns, ats, ips := cols(networks)
		batch.Queue(`
			UPDATE client_networks c SET last_seen = GREATEST(c.last_seen, v.at), ip_sample = v.ip, requests = c.requests + v.n
			FROM unnest($1::bigint[], $2::bigint[], $3::timestamptz[], $4::text[]) AS v(id, n, at, ip)
			WHERE c.id = v.id`, ids, ns, ats, ips)
	}
	return s.Pool.SendBatch(ctx, batch).Close()
}

// GetDevice returns one device.
func (s *Store) GetDevice(ctx context.Context, id int64) (Device, error) {
	d, err := scanDevice(s.Pool.QueryRow(ctx, `SELECT `+deviceCols+` FROM client_devices WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	return d, err
}

// ListDevices returns an employee's devices, most recently used first.
func (s *Store) ListDevices(ctx context.Context, employeeID int64) ([]Device, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+deviceCols+` FROM client_devices WHERE employee_id=$1 ORDER BY last_seen DESC`, employeeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetDeviceStatus changes a device's status; trusting records who confirmed it.
func (s *Store) SetDeviceStatus(ctx context.Context, id int64, status string, by *int64) error {
	if status == DeviceTrusted {
		_, err := s.Pool.Exec(ctx, `UPDATE client_devices SET status=$2, confirmed_at=now(), confirmed_by=$3 WHERE id=$1`, id, status, by)
		return err
	}
	_, err := s.Pool.Exec(ctx, `UPDATE client_devices SET status=$2 WHERE id=$1`, id, status)
	return err
}

// MarkDeviceNotified stamps notified_at (so the employee is told once).
func (s *Store) MarkDeviceNotified(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE client_devices SET notified_at=now() WHERE id=$1`, id)
	return err
}

// ExpirePendingDevices moves pending devices first seen before cutoff to expired.
func (s *Store) ExpirePendingDevices(ctx context.Context, cutoff time.Time) ([]Device, error) {
	rows, err := s.Pool.Query(ctx, `
		UPDATE client_devices SET status='expired'
		WHERE status='pending' AND first_seen < $1
		RETURNING `+deviceCols, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// CountActiveDevices counts an employee's devices used since `since`.
func (s *Store) CountActiveDevices(ctx context.Context, employeeID int64, since time.Time) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM client_devices WHERE employee_id=$1 AND last_seen >= $2 AND status <> 'blocked'`,
		employeeID, since).Scan(&n)
	return n, err
}

// CountTrustedDevices counts an employee's trusted devices.
func (s *Store) CountTrustedDevices(ctx context.Context, employeeID int64) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM client_devices WHERE employee_id=$1 AND status='trusted'`, employeeID).Scan(&n)
	return n, err
}

// UpsertNetwork records a use of a network, returning it and whether it is new.
func (s *Store) UpsertNetwork(ctx context.Context, n Network, at time.Time) (Network, bool, error) {
	var out Network
	var inserted bool
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO client_networks (employee_id, net_key, ip_sample, country, province, city, isp, datacenter, first_seen, last_seen, requests)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,1)
		ON CONFLICT (employee_id, net_key) DO UPDATE SET
			last_seen=GREATEST(client_networks.last_seen,EXCLUDED.last_seen),
			ip_sample=EXCLUDED.ip_sample, requests=client_networks.requests+1
		RETURNING id, employee_id, net_key, ip_sample, country, province, city, isp, datacenter, first_seen, last_seen, requests, (xmax = 0)`,
		n.EmployeeID, n.NetKey, n.IPSample, n.Country, n.Province, n.City, n.ISP, n.Datacenter, at).
		Scan(&out.ID, &out.EmployeeID, &out.NetKey, &out.IPSample, &out.Country, &out.Province, &out.City, &out.ISP,
			&out.Datacenter, &out.FirstSeen, &out.LastSeen, &out.Requests, &inserted)
	return out, inserted, err
}

// ListNetworks returns an employee's networks, most recent first.
func (s *Store) ListNetworks(ctx context.Context, employeeID int64, limit int) ([]Network, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, employee_id, net_key, ip_sample, country, province, city, isp, datacenter, first_seen, last_seen, requests
		FROM client_networks WHERE employee_id=$1 ORDER BY last_seen DESC LIMIT $2`, employeeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Network
	for rows.Next() {
		var n Network
		if err := rows.Scan(&n.ID, &n.EmployeeID, &n.NetKey, &n.IPSample, &n.Country, &n.Province, &n.City, &n.ISP,
			&n.Datacenter, &n.FirstSeen, &n.LastSeen, &n.Requests); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// AddRiskEvent records a risk event.
func (s *Store) AddRiskEvent(ctx context.Context, e RiskEvent) (RiskEvent, error) {
	if e.Detail == nil {
		e.Detail = map[string]any{}
	}
	d, _ := json.Marshal(e.Detail)
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO risk_events (employee_id, rule, severity, detail, device_id, network_id)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, at`,
		e.EmployeeID, e.Rule, e.Severity, d, e.DeviceID, e.NetworkID).Scan(&e.ID, &e.At)
	return e, err
}

// HasRecentRiskEvent reports whether an event with the rule (and optional device)
// was recorded for the employee since `since` — used to avoid repeating alerts.
func (s *Store) HasRecentRiskEvent(ctx context.Context, employeeID int64, rule string, deviceID *int64, since time.Time) (bool, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM risk_events
		WHERE employee_id=$1 AND rule=$2 AND at >= $3 AND ($4::bigint IS NULL OR device_id=$4)`,
		employeeID, rule, since, deviceID).Scan(&n)
	return n > 0, err
}

// RiskEventFilter narrows ListRiskEvents.
type RiskEventFilter struct {
	EmployeeID *int64
	Severity   string
	OpenOnly   bool
	Limit      int
}

// ListRiskEvents returns events newest first.
func (s *Store) ListRiskEvents(ctx context.Context, f RiskEventFilter) ([]RiskEvent, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT r.id, r.employee_id, COALESCE(e.name,''), r.rule, r.severity, r.detail, r.device_id, r.network_id,
			r.at, r.notified_at, r.resolved_at
		FROM risk_events r LEFT JOIN employees e ON e.id = r.employee_id
		WHERE ($1::bigint IS NULL OR r.employee_id=$1)
		  AND ($2 = '' OR r.severity=$2)
		  AND (NOT $3 OR r.resolved_at IS NULL)
		ORDER BY r.at DESC LIMIT $4`, f.EmployeeID, f.Severity, f.OpenOnly, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RiskEvent
	for rows.Next() {
		var e RiskEvent
		var d []byte
		if err := rows.Scan(&e.ID, &e.EmployeeID, &e.EmployeeName, &e.Rule, &e.Severity, &d, &e.DeviceID, &e.NetworkID,
			&e.At, &e.NotifiedAt, &e.ResolvedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(d, &e.Detail)
		out = append(out, e)
	}
	return out, rows.Err()
}

// ResolveRiskEvent marks an event resolved.
func (s *Store) ResolveRiskEvent(ctx context.Context, id int64, by *int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE risk_events SET resolved_at=now(), resolved_by=$2 WHERE id=$1 AND resolved_at IS NULL`, id, by)
	return err
}

// MarkRiskEventNotified stamps notified_at.
func (s *Store) MarkRiskEventNotified(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE risk_events SET notified_at=now() WHERE id=$1`, id)
	return err
}

// DailyUsage is one employee's request count and active hours for a day window.
type DailyUsage struct {
	EmployeeID  int64
	Requests    int64
	ActiveHours int
}

// UsageForWindow returns per-employee request counts and distinct active hours in [from,to).
func (s *Store) UsageForWindow(ctx context.Context, from, to time.Time) ([]DailyUsage, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT employee_id, COUNT(*), COUNT(DISTINCT date_trunc('hour', started_at))
		FROM requests WHERE started_at >= $1 AND started_at < $2
		GROUP BY employee_id`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailyUsage
	for rows.Next() {
		var u DailyUsage
		if err := rows.Scan(&u.EmployeeID, &u.Requests, &u.ActiveHours); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// MedianDailyRequests returns an employee's median daily request count over the
// days in [from,to) on which they were active (0 when no history).
func (s *Store) MedianDailyRequests(ctx context.Context, employeeID int64, from, to time.Time) (float64, int, error) {
	var median *float64
	var days int
	err := s.Pool.QueryRow(ctx, `
		SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY c), COUNT(*)
		FROM (SELECT date_trunc('day', started_at) d, COUNT(*) c FROM requests
		      WHERE employee_id=$1 AND started_at >= $2 AND started_at < $3 GROUP BY d) t`,
		employeeID, from, to).Scan(&median, &days)
	if err != nil {
		return 0, 0, err
	}
	if median == nil {
		return 0, days, nil
	}
	return *median, days, nil
}
