package wemanage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

const curationScheduleSchema = `
CREATE TABLE IF NOT EXISTS wm_curation_schedules(
 owner_id BIGINT PRIMARY KEY REFERENCES employees(id), enabled BOOLEAN NOT NULL DEFAULT false,
 hour INT NOT NULL DEFAULT 9 CHECK(hour BETWEEN 0 AND 23), minute INT NOT NULL DEFAULT 0 CHECK(minute BETWEEN 0 AND 59),
 last_day DATE, last_attempt TIMESTAMPTZ, last_run_id BIGINT REFERENCES wm_curation_runs(id),
 state TEXT NOT NULL DEFAULT 'idle', message TEXT NOT NULL DEFAULT '', updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS wm_curation_schedule_attempts(
 id BIGSERIAL PRIMARY KEY,owner_id BIGINT NOT NULL REFERENCES employees(id),day DATE NOT NULL,
 state TEXT NOT NULL DEFAULT 'running',run_id BIGINT REFERENCES wm_curation_runs(id),message TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),finished_at TIMESTAMPTZ,UNIQUE(owner_id,day)
);`

var curationZone = time.FixedZone("Asia/Shanghai", 8*60*60)

func (s *Service) curationSchedule(w http.ResponseWriter, r *http.Request) {
	owner := who(r).employee.ID
	var allowed bool
	if s.Store.Pool.QueryRow(r.Context(), `select is_super_admin and status='active' and private_work_since is null and name<>'林伊默' from employees where id=$1`, owner).Scan(&allowed) != nil || !allowed {
		fail(w, 403, "仅非私密管理员可管理自动整理")
		return
	}
	if r.Method == http.MethodPut {
		var q struct {
			Enabled bool `json:"enabled"`
			Hour    int  `json:"hour"`
			Minute  int  `json:"minute"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048)).Decode(&q) != nil || q.Hour < 0 || q.Hour > 23 || q.Minute < 0 || q.Minute > 59 {
			fail(w, 400, "请选择有效时间")
			return
		}
		_, err := s.Store.Pool.Exec(r.Context(), `insert into wm_curation_schedules(owner_id,enabled,hour,minute)values($1,$2,$3,$4) on conflict(owner_id)do update set enabled=excluded.enabled,hour=excluded.hour,minute=excluded.minute,updated_at=now()`, owner, q.Enabled, q.Hour, q.Minute)
		if err != nil {
			fail(w, 500, "保存定时设置失败")
			return
		}
	}
	var enabled bool
	hour, minute := 9, 0
	var day, state, message string
	var at *time.Time
	var run *int64
	err := s.Store.Pool.QueryRow(r.Context(), `select enabled,hour,minute,coalesce(last_day::text,''),last_attempt,last_run_id,state,message from wm_curation_schedules where owner_id=$1`, owner).Scan(&enabled, &hour, &minute, &day, &at, &run, &state, &message)
	if err != nil && err != pgx.ErrNoRows {
		fail(w, 500, "读取定时设置失败")
		return
	}
	reply(w, 200, map[string]any{"enabled": enabled, "hour": hour, "minute": minute, "timezone": "Asia/Shanghai", "last_day": day, "last_attempt": at, "last_run_id": run, "state": state, "message": message})
}

// Once-per-day claim is durable across restarts and competing service instances.
// Failures are visible, not retried indefinitely. A manual run remains available.
func (s *Service) CurationScheduleTick(ctx context.Context, now time.Time) error {
	local := now.In(curationZone)
	day := local.Format("2006-01-02")
	clock := local.Hour()*60 + local.Minute()
	// Expired attempts retain their day claim; a restart does not duplicate a model call.
	if _, err := s.Store.Pool.Exec(ctx, `update wm_curation_schedule_attempts set state='failed',message='任务中断或超时，可手动重试',finished_at=now() where state='running' and created_at<now()-interval '10 minutes'; update wm_curation_schedules s set state=a.state,message=a.message from wm_curation_schedule_attempts a where a.owner_id=s.owner_id and a.day=s.last_day and s.state='running' and a.state='failed'`); err != nil {
		return err
	}
	rows, err := s.Store.Pool.Query(ctx, `select s.owner_id from wm_curation_schedules s join employees e on e.id=s.owner_id where s.enabled and s.hour*60+s.minute<=$2 and (s.last_day is null or s.last_day<$1::date) and e.is_super_admin and e.status='active' and e.private_work_since is null and e.name<>'林伊默' order by s.owner_id`, day, clock)
	if err != nil {
		return err
	}
	owners := []int64{}
	for rows.Next() {
		var owner int64
		if err = rows.Scan(&owner); err != nil {
			rows.Close()
			return err
		}
		owners = append(owners, owner)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, owner := range owners {
		tx, err := s.Store.Pool.Begin(ctx)
		if err != nil {
			return err
		}
		var id int64
		// Recheck settings after acquiring the row lock; disabling prevents unclaimed work.
		err = tx.QueryRow(ctx, `select owner_id from wm_curation_schedules where owner_id=$1 and enabled and hour*60+minute<=$3 and (last_day is null or last_day<$2::date) for update skip locked`, owner, day, clock).Scan(&id)
		if err == pgx.ErrNoRows {
			tx.Rollback(ctx)
			continue
		}
		if err != nil {
			tx.Rollback(ctx)
			return err
		}
		var preparing bool
		err = tx.QueryRow(ctx, `select exists(select 1 from wm_knowledge_runs where status='running' and started_at>now()-interval '30 minutes')`).Scan(&preparing)
		if err != nil {
			tx.Rollback(ctx)
			return err
		}
		if preparing {
			_, err = tx.Exec(ctx, `update wm_curation_schedules set state='waiting',message='等待正在生成的日报/周报线索完成' where owner_id=$1`, owner)
			if err != nil {
				tx.Rollback(ctx)
				return err
			}
			if err = tx.Commit(ctx); err != nil {
				return err
			}
			continue
		}
		err = tx.QueryRow(ctx, `insert into wm_curation_schedule_attempts(owner_id,day)values($1,$2::date) on conflict do nothing returning id`, owner, day).Scan(&id)
		if err == pgx.ErrNoRows {
			tx.Rollback(ctx)
			continue
		}
		if err != nil {
			tx.Rollback(ctx)
			return err
		}
		_, err = tx.Exec(ctx, `update wm_curation_schedules set last_day=$2::date,last_attempt=now(),state='running',message='',last_run_id=null where owner_id=$1`, owner, day)
		if err != nil {
			tx.Rollback(ctx)
			return err
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
		go s.runScheduledCuration(owner, id, day)
	}
	return nil
}
func (s *Service) runScheduledCuration(owner, attempt int64, day string) {
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	out, err := s.queueCuration(ctx, owner)
	state, message := "done", "整理完成，等待人工审核"
	if err == nil && out.State == "running" {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for err == nil {
			var status, reason string
			err = s.Store.Pool.QueryRow(ctx, `select state,error from wm_curation_runs where id=$1`, out.ID).Scan(&status, &reason)
			if err != nil || status == "done" {
				break
			}
			if status == "failed" {
				err = fmt.Errorf("%s", reason)
				break
			}
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-ticker.C:
			}
		}
	} else if err == nil && out.Reused {
		state = "reused"
		message = "材料与模型未变化，复用已有结果，未重复调用模型"
	}
	if err != nil {
		state = "failed"
		message = "自动整理未完成，请查看整理结果或手动重试"
	}
	done, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	tx, e := s.Store.Pool.Begin(done)
	if e != nil {
		return
	}
	defer tx.Rollback(done)
	_, e = tx.Exec(done, `update wm_curation_schedule_attempts set state=$2,message=$3,run_id=nullif($4,0),finished_at=now() where id=$1 and state='running'`, attempt, state, message, out.ID)
	if e != nil {
		return
	}
	_, e = tx.Exec(done, `update wm_curation_schedules set state=$3,message=$4,last_run_id=nullif($5,0) where owner_id=$1 and last_day=$2::date and state='running'`, owner, day, state, message, out.ID)
	if e == nil {
		_ = tx.Commit(done)
	}
}
func (s *Service) RunCurationScheduler(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		work, cancel := context.WithTimeout(ctx, 20*time.Second)
		_ = s.CurationScheduleTick(work, time.Now())
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func (s *Service) curationHistory(w http.ResponseWriter, r *http.Request) {
	current, err := s.loadProjectCandidates(r.Context())
	if err != nil {
		fail(w, 500, "读取来源失败")
		return
	}
	rows, err := s.Store.Pool.Query(r.Context(), `select id,state,created_at,sources from wm_curation_runs where owner_id=$1 and exists(select 1 from employees where id=$1 and private_work_since is null and name<>'林伊默') order by id desc`, who(r).employee.ID)
	if err != nil {
		fail(w, 500, "读取历史失败")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var state string
		var at time.Time
		var raw []byte
		if rows.Scan(&id, &state, &at, &raw) != nil {
			fail(w, 500, "读取历史失败")
			return
		}
		var saved []*projectCandidate
		if json.Unmarshal(raw, &saved) == nil && curationInputsVisible(saved, current) {
			out = append(out, map[string]any{"id": id, "state": state, "created_at": at})
		}
	}
	if rows.Err() != nil {
		fail(w, 500, "读取历史失败")
		return
	}
	reply(w, 200, map[string]any{"runs": out})
}
