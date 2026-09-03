package demo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// State is what the demo tells its visitors about itself: whether it is usable
// right now, and when the data is next wiped.
type State struct {
	Status      string     `json:"status"` // READY or RESETTING
	LastResetAt *time.Time `json:"last_reset_at,omitempty"`
	NextResetAt *time.Time `json:"next_reset_at,omitempty"`
}

// Ready reports whether the demo can serve traffic.
func (s State) Ready() bool { return s.Status == statusReady }

const (
	statusReady     = "READY"
	statusResetting = "RESETTING"
	// resetLockID is the advisory lock that keeps two processes - the worker's
	// timer and a visitor pressing "reset the demo" - from rebuilding the demo
	// at the same time.
	resetLockID int64 = 867_972_151
)

// ErrResetInProgress is returned when a reset is already running.
var ErrResetInProgress = errors.New("demo reset already in progress")

// State reads the shared demo state.
func (r *Runner) State(ctx context.Context) (State, error) {
	var state State
	err := r.pool.QueryRow(ctx, `SELECT status,last_reset_at,next_reset_at FROM demo_state WHERE singleton`).
		Scan(&state.Status, &state.LastResetAt, &state.NextResetAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return State{Status: statusReady}, nil
	}
	if err != nil {
		return State{}, err
	}
	return state, nil
}

// DueForReset reports whether the scheduled reset time has passed. The worker
// polls this rather than holding its own timer, so a restart does not postpone
// the next reset and two workers cannot both decide it is time.
func (r *Runner) DueForReset(ctx context.Context) (bool, error) {
	if r.opts.ResetInterval <= 0 {
		return false, nil
	}
	state, err := r.State(ctx)
	if err != nil {
		return false, err
	}
	if state.Status == statusResetting {
		return false, nil
	}
	return state.NextResetAt == nil || !state.NextResetAt.After(time.Now()), nil
}

// markResetting flips the shared flag so the API can answer "the demo is being
// rebuilt" instead of failing with whatever error a half-purged company
// produces.
func (r *Runner) markResetting(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO demo_state (singleton,status,updated_at) VALUES (true,$1,now())
		ON CONFLICT (singleton) DO UPDATE SET status=$1,updated_at=now()`, statusResetting)
	return err
}

// markReady records the completed reset and schedules the next one.
func (r *Runner) markReady(ctx context.Context) error {
	var next any
	if r.opts.ResetInterval > 0 {
		next = time.Now().Add(r.opts.ResetInterval)
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO demo_state (singleton,status,last_reset_at,next_reset_at,updated_at)
		VALUES (true,$1,now(),$2,now())
		ON CONFLICT (singleton) DO UPDATE SET status=$1,last_reset_at=now(),next_reset_at=$2,updated_at=now()`,
		statusReady, next)
	return err
}

// withResetLock runs fn while holding the cluster-wide reset lock, so a
// scheduled reset and a visitor-triggered one can never overlap.
func (r *Runner) withResetLock(ctx context.Context, fn func(context.Context) error) error {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var acquired bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, resetLockID).Scan(&acquired); err != nil {
		return fmt.Errorf("acquire demo reset lock: %w", err)
	}
	if !acquired {
		return ErrResetInProgress
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, resetLockID)
	}()
	return fn(ctx)
}

// ensureSchedule fills in a missing next-reset time without disturbing one that
// is already set, so restarts neither postpone nor trigger a rebuild.
func (r *Runner) ensureSchedule(ctx context.Context) error {
	if r.opts.ResetInterval <= 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO demo_state (singleton,status,next_reset_at,updated_at) VALUES (true,$2,$1,now())
		ON CONFLICT (singleton) DO UPDATE SET next_reset_at=COALESCE(demo_state.next_reset_at,$1),updated_at=now()`,
		time.Now().Add(r.opts.ResetInterval), statusReady)
	return err
}
