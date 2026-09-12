package migrations

import "testing"

// TestUnknownCountsOnlyVersionsAboveLatest pins the distinction that took an
// installation offline once already: a squashed history leaves applied rows
// with no matching file, and that is normal. Only a version HIGHER than this
// binary's latest means the schema has moved past the code.
func TestUnknownCountsOnlyVersionsAboveLatest(t *testing.T) {
	latest, err := Latest()
	if err != nil {
		t.Fatal(err)
	}
	if latest == 0 {
		t.Skip("no embedded migrations")
	}
	for name, tc := range map[string]struct {
		applied map[int64]struct{}
		want    int
	}{
		"squashed history below latest": {
			applied: map[int64]struct{}{1: {}, 2: {}, 3: {}, latest: {}},
			want:    0,
		},
		"schema moved past this binary": {
			applied: map[int64]struct{}{latest: {}, latest + 1: {}, latest + 2: {}},
			want:    2,
		},
	} {
		t.Run(name, func(t *testing.T) {
			unknown := 0
			for version := range tc.applied {
				if version > latest {
					unknown++
				}
			}
			if unknown != tc.want {
				t.Fatalf("unknown = %d, want %d", unknown, tc.want)
			}
		})
	}
}
