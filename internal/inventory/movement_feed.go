package inventory

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/alpyxn/varyaone/internal/identity"
	"github.com/google/uuid"
)

type MovementFeedResult struct {
	Items      []any  `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}
type movementFeedCursor struct {
	PostedAt  time.Time
	ID, Scope string
}

// Merge two bounded, filtered streams using the same stable ordering. Grouped
// operation lines are excluded in SQL before limiting the standalone stream.
func (s *Service) ListMovementFeed(ctx context.Context, filter MovementListFilter, cursor string) (MovementFeedResult, error) {
	result := MovementFeedResult{Items: []any{}}
	filter, err := normalizeMovementListFilter(filter)
	if err != nil {
		return result, err
	}
	if filter.Limit > 100 {
		filter.Limit = 100
	}
	limit := filter.Limit
	raw, _ := json.Marshal(filter)
	scope := fmt.Sprintf("%x", sha256.Sum256(raw))
	if cursor != "" {
		var c movementFeedCursor
		data, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil || json.Unmarshal(data, &c) != nil || c.Scope != scope || c.PostedAt.IsZero() || uuid.Validate(c.ID) != nil {
			return result, fmt.Errorf("%w: sayfa bilgisi geçersiz", identity.ErrValidation)
		}
		filter.BeforeTime = &c.PostedAt
		filter.BeforeID = c.ID
	}
	filter.Limit = limit + 1
	operations, err := s.ListStockMovementOperations(ctx, filter)
	if err != nil {
		return result, err
	}
	filter.StandaloneOnly = true
	movements, err := s.ListMovementsFiltered(ctx, filter)
	if err != nil {
		return result, err
	}
	type entry struct {
		id   string
		date time.Time
		item any
	}
	entries := make([]entry, 0, len(operations)+len(movements))
	for _, o := range operations {
		entries = append(entries, entry{o.ID, o.PostedAt, o})
	}
	for _, m := range movements {
		entries = append(entries, entry{m.ID, m.PostedAt, m})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].date.Equal(entries[j].date) {
			return entries[i].id > entries[j].id
		}
		return entries[i].date.After(entries[j].date)
	})
	if len(entries) > limit {
		entries = entries[:limit]
		last := entries[limit-1]
		data, _ := json.Marshal(movementFeedCursor{last.date, last.id, scope})
		result.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	for _, e := range entries {
		result.Items = append(result.Items, e.item)
	}
	return result, nil
}
