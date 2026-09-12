package backup

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Completeness: what an archive actually contains, stated rather than implied.
//
// The old manifest had one way to say a backup was incomplete — a non-empty
// SkippedObjects list — and nothing that read it treated the list as a failure.
// Worse, the list could only record objects that disappeared during the walk.
// An installation whose storage volume was not mounted produced an archive with
// zero objects, zero skips, and every checksum correct: indistinguishable from
// a complete backup of an installation with no files.
//
// StorageMode closes that gap by recording what the producer believed it was
// doing, so a reader can tell "this installation has no files" from "this
// backup does not contain the files".

// StorageMode describes the storage half of an archive.
type StorageMode string

const (
	// StorageFull means the local storage tree was captured in full.
	StorageFull StorageMode = "full"
	// StorageDatabaseOnly means storage was deliberately not captured: the
	// installation keeps objects somewhere this engine does not back up (an
	// object-store provider), or has no storage configured at all.
	StorageDatabaseOnly StorageMode = "database-only"
	// StorageDegraded means storage capture was attempted and did not complete.
	// Such an archive is still worth keeping and must never be offered as a
	// full recovery point.
	StorageDegraded StorageMode = "degraded"
)

// ErrIncompleteBackup is returned where only a complete archive will do — the
// safety backup taken before a restore, and the pre-deploy recovery point.
// Those two exist to be restored from; accepting a partial one there would mean
// discovering the gap at the moment it cannot be fixed.
var ErrIncompleteBackup = errors.New("yedek eksik: tam kurtarma noktası olarak kullanılamaz")

// StorageMode reports the archive's storage completeness, inferring it for
// archives written before the field existed.
//
// The inference is deliberately conservative. An old archive that recorded
// skipped objects is degraded, which it plainly is. An old archive with no
// skips is reported as full, because that is what the producer meant at the
// time — but the unmounted-volume case above is exactly what it cannot
// distinguish, which is why new archives state it instead of implying it.
func (m Manifest) StorageModeOrInferred() StorageMode {
	if m.StorageMode != "" {
		return m.StorageMode
	}
	if len(m.SkippedObjects) > 0 {
		return StorageDegraded
	}
	return StorageFull
}

// Complete reports whether this archive is a full recovery point for the whole
// installation.
func (m Manifest) Complete() bool {
	return m.StorageModeOrInferred() != StorageDegraded && len(m.SkippedObjects) == 0
}

// RequireComplete returns ErrIncompleteBackup unless the archive is a full
// recovery point.
func (m Manifest) RequireComplete() error {
	if m.Complete() {
		return nil
	}
	reasons := []string{}
	if mode := m.StorageModeOrInferred(); mode == StorageDegraded {
		reasons = append(reasons, "depolama görüntüsü tamamlanamadı")
	}
	if n := len(m.SkippedObjects); n > 0 {
		reasons = append(reasons, fmt.Sprintf("%d dosya alınamadı", n))
	}
	return fmt.Errorf("%w (%s)", ErrIncompleteBackup, strings.Join(reasons, ", "))
}

// storagePreflight decides what the storage half of this backup will be, and
// refuses to proceed when the answer looks like misconfiguration rather than
// intent.
//
// The distinction it draws is the whole point: "this installation stores no
// files" and "this installation's files are not where I was told to look" are
// indistinguishable after the fact, and only one of them is safe to record as a
// complete backup.
func (e *Engine) storagePreflight() (StorageMode, error) {
	provider := e.storageProvider
	if provider != "" && provider != "local" {
		// Objects live in a service this engine does not read. Say so in the
		// archive rather than shipping one that appears to contain everything.
		return StorageDatabaseOnly, nil
	}
	if e.storageRoot == "" {
		return StorageDatabaseOnly, nil
	}
	info, err := os.Stat(e.storageRoot)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "", fmt.Errorf("depolama kökü bulunamadı: %s "+
			"(birim bağlanmadıysa alınacak yedek dosyaları içermez)", e.storageRoot)
	case err != nil:
		return "", fmt.Errorf("depolama kökü okunamadı: %w", err)
	case !info.IsDir():
		return "", fmt.Errorf("depolama kökü bir dizin değil: %s", e.storageRoot)
	}
	// Readable is not enough: the snapshot needs to create a working directory
	// inside the root, and finding that out after pausing every writer would
	// turn a configuration problem into an outage.
	probe, err := os.MkdirTemp(e.storageRoot, ".varya-preflight-")
	if err != nil {
		return "", fmt.Errorf("depolama köküne yazılamıyor: %w", err)
	}
	_ = os.RemoveAll(probe)
	return StorageFull, nil
}
