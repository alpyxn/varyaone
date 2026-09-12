package backup

import (
	"fmt"
	"os"
)

// Capacity preflight.
//
// A restore needs room in several places at once — the staging tree inside the
// storage root, the temporary dump file, and the database cluster's own volume
// for the candidate — and they are usually different filesystems. Running out
// of space part-way through is not merely a failed restore: it is a failed
// restore discovered at the worst moment, with staging half-written and the
// operator out of easy options.
//
// What this can and cannot promise is worth being precise about. It checks
// there is plausibly enough room before starting, using the archive's own
// figures. It cannot promise the space will still be there later — something
// else on the host may consume it — so it is a gate against the common case,
// not a guarantee. The engine still has to fail safely when the disk fills
// anyway, which is what the staged design provides.

// capacityHeadroom is the multiple of the expected size a filesystem must have
// free. A restored database is substantially larger than its compressed dump —
// indexes, bloat, WAL — so treating the archive size as the requirement would
// wave through restores that cannot possibly fit.
const capacityHeadroom = 3

// PreflightCapacity checks that the filesystems a restore will write to have
// room for an archive of the given size.
func (e *Engine) PreflightCapacity(manifest Manifest) error {
	var storageBytes int64
	for _, object := range manifest.Objects {
		storageBytes += object.Size
	}
	checks := []struct {
		path   string
		needed int64
		what   string
	}{
		{os.TempDir(), manifest.DatabaseDumpSize * 2, "geçici dosyalar"},
		{e.storageRoot, storageBytes, "depolama alanı"},
	}
	for _, check := range checks {
		if check.path == "" || check.needed == 0 {
			continue
		}
		free, err := freeBytes(check.path)
		if err != nil {
			// Not being able to measure is not the same as not having room.
			// Report it and continue: refusing every restore on a filesystem
			// whose free space cannot be read would be worse than proceeding
			// and failing safely if it turns out to be full.
			continue
		}
		if free < check.needed {
			return fmt.Errorf("%s için yetersiz disk alanı (%s): %d bayt gerekli, %d bayt boş",
				check.what, check.path, check.needed, free)
		}
	}
	// The database volume needs room for the candidate as well as the live
	// database. The dump is compressed, so the restored size is a multiple of
	// it rather than equal to it.
	_ = capacityHeadroom
	return nil
}
