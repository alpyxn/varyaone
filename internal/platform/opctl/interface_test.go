package opctl

import (
	"testing"

	"github.com/alpyxn/varyaone/internal/backup"
)

// A Lease must satisfy the engine's journal interface. This is the only place
// the two packages meet, and it is a compile-time check so a signature change
// on either side cannot quietly stop the restore path from being recorded.
func TestLeaseSatisfiesRestoreJournal(t *testing.T) {
	var _ backup.RestoreJournal = (*Lease)(nil)
}
