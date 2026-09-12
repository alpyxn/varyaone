package backup

// Windows cannot open a directory for synchronisation the way Unix can, and
// NTFS orders the metadata write behind the file data it refers to, so the
// rename cannot land before the bytes it publishes.
func syncDir(string) error { return nil }
