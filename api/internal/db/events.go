package db

import (
	"context"
	"encoding/json"
	"fmt"
)

// WriteEvent appends one row to the web.application_events audit trail,
// JSON-marshaling payload (pass a map or struct). q may be a *pgxpool.Pool or a
// pgx.Tx, so a caller can fold the event into a surrounding transaction.
func WriteEvent(ctx context.Context, q Querier, sysProfile, jobID, eventType string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal %s payload: %w", eventType, err)
	}
	if _, err := q.Exec(ctx, `
        INSERT INTO web.application_events (sys_profile, job_id, event_type, payload)
        VALUES ($1, $2, $3, $4::jsonb)`, sysProfile, jobID, eventType, b); err != nil {
		return fmt.Errorf("write %s event: %w", eventType, err)
	}
	return nil
}
