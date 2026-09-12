package backup

import (
	"context"
	"fmt"
	"os"

	"github.com/alpyxn/varyaone/internal/platform/migrations"
)

// Inspection: saying what a check actually proved.
//
// "Checksum passed" was being used to mean "this backup is good", and those are
// different claims. A SHA-256 match proves the bytes are the bytes the manifest
// describes. It does not prove the archive came from a trusted producer — an
// attacker who can change the archive can change the manifest with it — nor
// that restoring it will work, nor that it contains everything.
//
// So the report has separate axes, and each says what it is and is not. The one
// that cannot be answered from the file at all is restorability: the only thing
// that proves an archive restores is restoring it.

// IntegrityState describes one dimension of an inspection.
type IntegrityState string

const (
	StateOK      IntegrityState = "ok"
	StateFailed  IntegrityState = "failed"
	StateUnknown IntegrityState = "unknown"
)

// Report is the result of inspecting an archive.
type Report struct {
	Path string `json:"path,omitempty"`
	Size int64  `json:"size"`

	// Bytes reports whether every checksum in the manifest matched. This is
	// the only axis a checksum pass speaks to.
	Bytes       IntegrityState `json:"bytes"`
	BytesDetail string         `json:"bytes_detail"`

	// Provenance reports whether the producer can be established. It is
	// currently always unknown: the format carries no signature, so a manifest
	// and its archive can be rewritten together and still verify perfectly.
	Provenance       IntegrityState `json:"provenance"`
	ProvenanceDetail string         `json:"provenance_detail"`

	// Compatibility reports whether this binary can load the archive: schema
	// version, master key fingerprint, PostgreSQL major.
	Compatibility       IntegrityState `json:"compatibility"`
	CompatibilityDetail string         `json:"compatibility_detail"`

	// Completeness reports whether the archive covers the whole installation.
	Completeness       IntegrityState `json:"completeness"`
	CompletenessDetail string         `json:"completeness_detail"`

	// RestoreProof reports whether this archive has actually been restored
	// somewhere. Nothing in the file can establish it, so it is unknown until
	// a restore rehearsal says otherwise.
	RestoreProof       IntegrityState `json:"restore_proof"`
	RestoreProofDetail string         `json:"restore_proof_detail"`

	Manifest Manifest `json:"manifest"`
}

// Usable reports whether the archive is worth attempting a restore from. It is
// deliberately not called "restorable".
func (r Report) Usable() bool {
	return r.Bytes == StateOK && r.Compatibility != StateFailed
}

// Inspect reads an archive and reports each dimension separately.
func (e *Engine) Inspect(ctx context.Context, path string) (Report, error) {
	report := Report{
		Path:               path,
		Provenance:         StateUnknown,
		ProvenanceDetail:   "arşiv imzalı değil: doğru sağlama, arşivin güvenilir bir kaynaktan geldiğini kanıtlamaz",
		RestoreProof:       StateUnknown,
		RestoreProofDetail: "bu arşivden gerçekten geri yükleme yapıldığına dair kayıt yok; tek kanıt bir geri yükleme provasıdır",
	}
	if info, err := os.Stat(path); err == nil {
		report.Size = info.Size()
	}

	file, err := os.Open(path)
	if err != nil {
		return report, err
	}
	defer func() { _ = file.Close() }()

	manifest, verifyErr := e.Verify(ctx, file)
	report.Manifest = manifest
	if verifyErr != nil {
		report.Bytes = StateFailed
		report.BytesDetail = verifyErr.Error()
		report.Compatibility = StateUnknown
		report.CompatibilityDetail = "arşiv okunamadığı için değerlendirilemedi"
		report.Completeness = StateUnknown
		report.CompletenessDetail = "arşiv okunamadığı için değerlendirilemedi"
		return report, nil
	}
	report.Bytes = StateOK
	report.BytesDetail = fmt.Sprintf("manifest + döküm + %d obje sağlaması doğrulandı", len(manifest.Objects))

	report.Compatibility, report.CompatibilityDetail = e.compatibility(manifest)
	report.Completeness, report.CompletenessDetail = completeness(manifest)
	return report, nil
}

func (e *Engine) compatibility(manifest Manifest) (IntegrityState, string) {
	latest, err := migrations.Latest()
	if err != nil {
		return StateUnknown, "bu sürümün şema listesi okunamadı"
	}
	if manifest.MigrationVersion > latest {
		return StateFailed, fmt.Sprintf(
			"yedek şeması bu sürümden yeni (yedek=%d, bu sürüm=%d): önce uygulamayı yükseltin",
			manifest.MigrationVersion, latest)
	}
	if e.keyFingerprint != "" && manifest.MasterKeyFingerprint != "" &&
		e.keyFingerprint != manifest.MasterKeyFingerprint {
		return StateFailed, "yedek farklı bir VARYAONE_MASTER_KEY ile alınmış: şifreli alanlar okunamaz"
	}
	if manifest.MasterKeyFingerprint == "" {
		// Old archives carry no fingerprint. That is not proof the key matches.
		return StateUnknown, "yedekte anahtar parmak izi yok: anahtar uyumu doğrulanamadı"
	}
	detail := fmt.Sprintf("şema %d ≤ %d, anahtar eşleşiyor", manifest.MigrationVersion, latest)
	if manifest.PostgresServerNum > 0 {
		detail += fmt.Sprintf(", kaynak PostgreSQL %d", manifest.PostgresServerNum/10000)
	}
	return StateOK, detail
}

func completeness(manifest Manifest) (IntegrityState, string) {
	switch mode := manifest.StorageModeOrInferred(); {
	case len(manifest.SkippedObjects) > 0:
		return StateFailed, fmt.Sprintf("%d dosya alınamamış: tam kurtarma noktası değil", len(manifest.SkippedObjects))
	case mode == StorageDegraded:
		return StateFailed, "depolama görüntüsü tamamlanamamış: tam kurtarma noktası değil"
	case mode == StorageDatabaseOnly:
		return StateUnknown, "yalnız veritabanı: dosyalar bu arşivde değil (sağlayıcı: " + providerOrUnknown(manifest) + ")"
	case manifest.StorageMode == "":
		// Inferred, not stated. An old archive taken while the storage volume
		// was unmounted looks exactly like this one.
		return StateUnknown, "eski biçim: dosyaların eksiksizliği arşivden doğrulanamıyor"
	default:
		return StateOK, fmt.Sprintf("veritabanı + %d dosya", len(manifest.Objects))
	}
}

func providerOrUnknown(manifest Manifest) string {
	if manifest.StorageProvider == "" {
		return "bilinmiyor"
	}
	return manifest.StorageProvider
}
