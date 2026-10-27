package snapshot

import "time"

// Op is one recorded change DSDock made, kept for the operation history and
// for diagnosing failed changes later.
type Op struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Home       string    `json:"home"`
	Profile    string    `json:"profile"`
	Spec       string    `json:"spec,omitempty"`
	Status     string    `json:"status"`
	SnapshotID string    `json:"snapshotId,omitempty"`
	Log        string    `json:"log,omitempty"`
	StartedAt  time.Time `json:"startedAt"`
	EndedAt    time.Time `json:"endedAt,omitzero"`
}

// Op statuses.
const (
	OpRunning    = "running"
	OpApplied    = "applied"
	OpRolledBack = "rolled-back"
	OpFailed     = "failed" // the change failed and so did the rollback
	OpRefused    = "refused"
)

// BeginOp records that a change started.
func (s *Store) BeginOp(kind, home, name, spec string) (*Op, error) {
	op := &Op{ID: NewID("o"), Kind: kind, Home: home, Profile: name, Spec: spec, Status: OpRunning, StartedAt: time.Now().UTC()}
	_, err := s.db.Exec(`INSERT INTO ops (id, kind, dsh_home, profile, spec, status, started_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		op.ID, kind, home, name, spec, op.Status, op.StartedAt.UnixMilli())
	return op, err
}

// EndOp stores the outcome. The log must already be redacted.
func (s *Store) EndOp(op *Op) error {
	op.EndedAt = time.Now().UTC()
	_, err := s.db.Exec(`UPDATE ops SET status = ?, snapshot_id = ?, log = ?, ended_at = ? WHERE id = ?`,
		op.Status, op.SnapshotID, op.Log, op.EndedAt.UnixMilli(), op.ID)
	return err
}

// Ops lists recent operations for a profile, newest first. An empty profile
// lists every profile in home.
func (s *Store) Ops(home, name string, limit int) ([]*Op, error) {
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT id, kind, dsh_home, profile, spec, status, snapshot_id, log, started_at, ended_at FROM ops WHERE dsh_home = ?`
	args := []any{home}
	if name != "" {
		q += ` AND profile = ?`
		args = append(args, name)
	}
	q += ` ORDER BY started_at DESC, id DESC LIMIT ?`
	rows, err := s.db.Query(q, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Op
	for rows.Next() {
		var op Op
		var started, ended int64
		if err := rows.Scan(&op.ID, &op.Kind, &op.Home, &op.Profile, &op.Spec, &op.Status, &op.SnapshotID, &op.Log, &started, &ended); err != nil {
			return nil, err
		}
		op.StartedAt = time.UnixMilli(started).UTC()
		if ended != 0 {
			op.EndedAt = time.UnixMilli(ended).UTC()
		}
		out = append(out, &op)
	}
	return out, rows.Err()
}
