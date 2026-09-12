package backup

import (
	"archive/tar"
	"errors"
	"fmt"
	"path"
	"runtime"
	"strings"
)

// Archive limits. They exist so a hostile or corrupt `.varya` cannot make the
// engine allocate unbounded memory or create an unbounded number of files
// before a single checksum has been compared.
const (
	// maxManifestBytes caps the JSON manifest. Real manifests are one line per
	// storage object; 256 MiB is several million objects' worth.
	maxManifestBytes = 256 << 20
	// maxObjects caps how many storage entries a manifest may declare.
	maxObjects = 2_000_000
	// maxKeyLength caps a single storage key.
	maxKeyLength = 1024
)

// ErrArchiveInvalid marks every rejection that comes from parsing the archive
// itself (bad manifest, bad path, unsupported entry, budget exceeded). It is
// always raised before anything live has been touched.
var ErrArchiveInvalid = errors.New("geçersiz .varya arşivi")

func invalidArchive(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrArchiveInvalid, fmt.Sprintf(format, args...))
}

// validateObjectKey is the single gate every storage key passes through, on
// both the write side (snapshotStorage) and the read side (Verify, Restore).
//
// A key is a canonical, relative, slash-separated path. Anything that could
// escape the destination directory, collide with the engine's own working
// directories, or mean something different on another platform is rejected
// here rather than being handed to the filesystem. A correct checksum on an
// entry says nothing about where that entry wants to be written, so this check
// is deliberately independent of checksum verification.
func validateObjectKey(key string) error {
	switch {
	case key == "":
		return invalidArchive("boş depolama anahtarı")
	case len(key) > maxKeyLength:
		return invalidArchive("depolama anahtarı çok uzun (%d bayt)", len(key))
	case strings.ContainsRune(key, 0):
		return invalidArchive("depolama anahtarı NUL içeriyor")
	case strings.ContainsRune(key, '\\'):
		return invalidArchive("depolama anahtarı ters bölü içeriyor: %q", key)
	case strings.HasPrefix(key, "/"):
		return invalidArchive("mutlak depolama anahtarı: %q", key)
	case len(key) >= 2 && key[1] == ':':
		// "C:foo" and "C:/foo" are drive-relative on Windows.
		return invalidArchive("sürücü harfi içeren depolama anahtarı: %q", key)
	case path.Clean(key) != key:
		// Catches "a//b", "a/./b", "a/", "..", "./a" in one comparison.
		return invalidArchive("kanonik olmayan depolama anahtarı: %q", key)
	}
	for _, segment := range strings.Split(key, "/") {
		switch {
		case segment == "" || segment == "." || segment == "..":
			return invalidArchive("geçersiz yol parçası içeren depolama anahtarı: %q", key)
		case isInternalName(segment):
			// `.varya-*` names belong to the engine's own snapshot / staging /
			// retired directories. An archive must never be able to write into
			// or shadow one.
			return invalidArchive("ayrılmış %s* adı içeren depolama anahtarı: %q", internalPrefix, key)
		case strings.HasSuffix(segment, " ") || strings.HasSuffix(segment, "."):
			// Windows silently strips these, so "x " and "x" would be the same
			// file there and different files here.
			return invalidArchive("boşluk veya nokta ile biten yol parçası: %q", key)
		case isReservedWindowsName(segment):
			return invalidArchive("Windows'ta ayrılmış ad içeren depolama anahtarı: %q", key)
		}
		if runtime.GOOS == "windows" && strings.ContainsAny(segment, `<>:"|?*`) {
			return invalidArchive("Windows'ta geçersiz karakter içeren depolama anahtarı: %q", key)
		}
	}
	return nil
}

// isReservedWindowsName reports whether a path segment is one of the DOS device
// names. They are reserved with any extension ("CON", "con.txt") and are
// rejected on every platform so an archive written on Linux stays restorable on
// Windows.
func isReservedWindowsName(segment string) bool {
	base, _, _ := strings.Cut(segment, ".")
	switch strings.ToUpper(base) {
	case "CON", "PRN", "AUX", "NUL":
		return true
	case "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return true
	}
	return false
}

// validateManifest checks the manifest's own consistency before any of it is
// used to size a buffer, create a file or trust a checksum.
func validateManifest(manifest Manifest) error {
	if manifest.FormatVersion != FormatVersion {
		return invalidArchive("desteklenmeyen yedek format sürümü %d", manifest.FormatVersion)
	}
	if manifest.DatabaseDumpSize < 0 {
		return invalidArchive("negatif veritabanı dökümü boyutu %d", manifest.DatabaseDumpSize)
	}
	if !isSHA256Hex(manifest.DatabaseDumpSHA256) {
		return invalidArchive("veritabanı dökümü sağlaması geçersiz biçimde")
	}
	if len(manifest.Objects) > maxObjects {
		return invalidArchive("manifest %d obje içeriyor (sınır %d)", len(manifest.Objects), maxObjects)
	}
	seen := make(map[string]struct{}, len(manifest.Objects))
	for _, object := range manifest.Objects {
		if err := validateObjectKey(object.Key); err != nil {
			return err
		}
		if object.Size < 0 {
			return invalidArchive("depolama objesi %q negatif boyutta", object.Key)
		}
		if !isSHA256Hex(object.SHA256) {
			return invalidArchive("depolama objesi %q sağlaması geçersiz biçimde", object.Key)
		}
		if _, duplicate := seen[object.Key]; duplicate {
			return invalidArchive("manifest %q anahtarını iki kez içeriyor", object.Key)
		}
		seen[object.Key] = struct{}{}
	}
	// A key may not also be a directory prefix of another key: "a/b" and "a"
	// cannot both exist on a filesystem.
	for key := range seen {
		for prefix := path.Dir(key); prefix != "." && prefix != "/"; prefix = path.Dir(prefix) {
			if _, clash := seen[prefix]; clash {
				return invalidArchive("depolama anahtarı %q hem dosya hem dizin olarak geçiyor", prefix)
			}
		}
	}
	return nil
}

func isSHA256Hex(value string) bool {
	if len(value) != 64 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// storageEntryKey classifies one tar header from the storage section of the
// archive. It returns the validated key, or ok=false for an entry that is not
// storage content at all.
//
// Only regular files are accepted. Symlinks, hard links, devices, FIFOs and
// directory entries are rejected outright rather than skipped: an archive
// containing one was not produced by this engine, and silently ignoring it
// would let a tampered archive pass a "nothing unexpected here" reading.
func storageEntryKey(header *tar.Header) (key string, ok bool, err error) {
	if !strings.HasPrefix(header.Name, storagePrefix) {
		// Trailing non-storage entries are how older producers appended a JSON
		// log line to the archive stream. They carry no storage content, so
		// they are ignored here and reported by the caller's completeness check.
		return "", false, nil
	}
	switch header.Typeflag {
	case tar.TypeReg:
	case tar.TypeSymlink, tar.TypeLink:
		return "", false, invalidArchive("arşiv bağlantı girdisi içeriyor: %q", header.Name)
	case tar.TypeDir:
		return "", false, invalidArchive("arşiv dizin girdisi içeriyor: %q", header.Name)
	default:
		return "", false, invalidArchive("desteklenmeyen arşiv girdi türü %q: %q", string(header.Typeflag), header.Name)
	}
	key = strings.TrimPrefix(header.Name, storagePrefix)
	if err := validateObjectKey(key); err != nil {
		return "", false, err
	}
	return key, true, nil
}
