package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// catalogRelPath is appended to a configured catalog URL only if that URL does
// not already end in .json — kept simple here because every URL varyaone ships
// with points straight at the document.
const maxCatalogBytes = 1 << 20 // 1 MiB; a release-notes/history doc is a few KB.

var sha256HexRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// catalogEntry is one channel's (or one history item's) entry in latest.json.
// Field-for-field the same shape as LatestInfo; kept as a separate type so the
// wire format can be validated before it becomes trusted data.
type catalogEntry struct {
	Version            string `json:"version"`
	NotesMD            string `json:"notes_md,omitempty"`
	Mandatory          bool   `json:"mandatory,omitempty"`
	MinVersion         string `json:"min_version,omitempty"`
	PublishedAt        string `json:"published_at,omitempty"`
	WindowsArtifactURL string `json:"windows_artifact_url,omitempty"`
	WindowsSHA256      string `json:"windows_sha256,omitempty"`
	PGMajor            int    `json:"pg_major,omitempty"`
}

// catalogDoc is the full latest.json contract.
type catalogDoc struct {
	SchemaVersion int                     `json:"schema_version"`
	Channels      map[string]catalogEntry `json:"channels"`
	Yanked        []string                `json:"yanked,omitempty"`
	History       []catalogEntry          `json:"history,omitempty"`
}

// toLatestInfo converts a wire entry into the trusted LatestInfo the rest of
// the package works with. artifactPrefix pins where a Windows artifact may be
// hosted; an entry whose URL does not match it (or whose sha256 is not a
// well-formed 64-hex digest) has its artifact fields dropped rather than
// rejecting the whole entry — the version/notes/mandatory fields are still
// useful, and the real download-integrity boundary is the updater's sha256
// verification against whatever survives here.
func (e catalogEntry) toLatestInfo(artifactPrefix string) *LatestInfo {
	if !isSemver(e.Version) {
		return nil
	}
	info := &LatestInfo{
		Version:     e.Version,
		NotesMD:     e.NotesMD,
		Mandatory:   e.Mandatory,
		MinVersion:  e.MinVersion,
		PublishedAt: e.PublishedAt,
		PGMajor:     e.PGMajor,
	}
	if e.WindowsArtifactURL == "" || e.WindowsSHA256 == "" {
		return info
	}
	if artifactPrefix != "" && !strings.HasPrefix(e.WindowsArtifactURL, artifactPrefix) {
		return info
	}
	if !sha256HexRE.MatchString(strings.ToLower(e.WindowsSHA256)) {
		return info
	}
	info.WindowsArtifactURL = e.WindowsArtifactURL
	info.WindowsSHA256 = strings.ToLower(e.WindowsSHA256)
	return info
}

// isYanked reports whether the catalog has withdrawn version.
func (d catalogDoc) isYanked(version string) bool {
	for _, v := range d.Yanked {
		if v == version {
			return true
		}
	}
	return false
}

// channel returns the trusted LatestInfo for channel name, or nil if absent,
// unparseable, or yanked. artifactPrefix is forwarded to toLatestInfo.
func (d catalogDoc) channel(name, artifactPrefix string) *LatestInfo {
	entry, ok := d.Channels[name]
	if !ok {
		return nil
	}
	if d.isYanked(entry.Version) {
		return nil
	}
	return entry.toLatestInfo(artifactPrefix)
}

// notesFor finds the release notes for version, checking the live channels
// first (the common case: asking about the version we're about to apply, or
// just applied) and falling back to history.
func (d catalogDoc) notesFor(version string) string {
	for _, entry := range d.Channels {
		if entry.Version == version {
			return entry.NotesMD
		}
	}
	for _, entry := range d.History {
		if entry.Version == version {
			return entry.NotesMD
		}
	}
	return ""
}

// fetchCatalogFrom fetches and parses exactly one URL. It is deliberately
// strict about the response shape: a redirect off https, an oversized body, a
// non-JSON content type, or an unrecognised schema version are all treated as
// "this source is not usable right now" so a caller iterating multiple URLs
// (fetchCatalog) moves on rather than trusting a mangled response.
func fetchCatalogFrom(ctx context.Context, client *http.Client, catalogURL, userAgent string) (*catalogDoc, error) {
	// Cache-bust: a stale CDN/proxy copy of latest.json would delay a kill
	// switch (a re-uploaded asset meant to withdraw a bad release) until the
	// cache naturally expires.
	sep := "?"
	if strings.Contains(catalogURL, "?") {
		sep = "&"
	}
	bustedURL := fmt.Sprintf("%s%s_=%d", catalogURL, sep, time.Now().Unix())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, bustedURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "application/json")
	req.Header.Set("cache-control", "no-cache")
	req.Header.Set("pragma", "no-cache")
	req.Header.Set("user-agent", userAgent)

	httpsOnlyClient := *client
	redirects := 0
	httpsOnlyClient.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		redirects++
		if redirects > 5 {
			return errors.New("too many redirects")
		}
		if r.URL.Scheme != "https" {
			return fmt.Errorf("refusing non-https redirect to %s", r.URL)
		}
		return nil
	}

	resp, err := httpsOnlyClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("catalog %s returned %s", catalogURL, resp.Status)
	}
	// Only HTML is refused up front. Neither host that actually serves this
	// catalog labels it as JSON — a release asset comes back as
	// application/octet-stream and raw.githubusercontent.com as text/plain —
	// so demanding "json" here rejected every real catalog and no installation
	// ever saw a release. HTML is still worth refusing early: a captive portal
	// or a GitHub error page is the one body whose parse error would otherwise
	// be reported as a malformed catalog. Everything else is settled by the
	// strict JSON decode below, which is the real gate.
	if ct := resp.Header.Get("content-type"); strings.Contains(strings.ToLower(ct), "text/html") {
		return nil, fmt.Errorf("catalog %s returned content-type %q", catalogURL, ct)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxCatalogBytes {
		return nil, fmt.Errorf("catalog %s exceeds %d bytes", catalogURL, maxCatalogBytes)
	}

	var doc catalogDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("catalog %s: parse: %w", catalogURL, err)
	}
	if doc.SchemaVersion != 1 {
		// Forward compatibility: a newer schema this build doesn't understand
		// yet is not an error, just unusable — behave as if nothing was found.
		return nil, nil
	}
	return &doc, nil
}

// fetchCatalog tries each URL in order and returns the first one that yields a
// usable document. Errors from individual sources are folded into the final
// error only if every source fails.
func fetchCatalog(ctx context.Context, client *http.Client, catalogURLs []string, userAgent string) (*catalogDoc, error) {
	var errs []error
	for _, u := range catalogURLs {
		doc, err := fetchCatalogFrom(ctx, client, u, userAgent)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		return doc, nil
	}
	if len(errs) == 0 {
		return nil, errors.New("no release catalog URLs configured")
	}
	return nil, errors.Join(errs...)
}
