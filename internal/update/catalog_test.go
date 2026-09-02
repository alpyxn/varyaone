package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testArtifactPrefix = "https://github.com/alpyxn/varyaone/releases/download/"

func jsonServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetchCatalog_ValidDocument(t *testing.T) {
	srv := jsonServer(t, `{
		"schema_version": 1,
		"channels": {"stable": {"version": "v1.5.0", "notes_md": "notes",
			"windows_artifact_url": "`+testArtifactPrefix+`v1.5.0/varyaone.zip",
			"windows_sha256": "`+strings.Repeat("a", 64)+`"}}
	}`)
	doc, err := fetchCatalog(context.Background(), srv.Client(), []string{srv.URL}, "test")
	if err != nil {
		t.Fatalf("fetchCatalog: %v", err)
	}
	info := doc.channel("stable", testArtifactPrefix)
	if info == nil || info.Version != "v1.5.0" || info.WindowsArtifactURL == "" {
		t.Fatalf("channel() = %+v", info)
	}
}

func TestFetchCatalog_UnknownSchemaVersion(t *testing.T) {
	srv := jsonServer(t, `{"schema_version": 2, "channels": {}}`)
	doc, err := fetchCatalog(context.Background(), srv.Client(), []string{srv.URL}, "test")
	if err != nil {
		t.Fatalf("fetchCatalog: %v", err)
	}
	if doc != nil {
		t.Fatalf("expected nil doc for unknown schema, got %+v", doc)
	}
}

func TestCatalogDoc_MissingChannel(t *testing.T) {
	doc := &catalogDoc{SchemaVersion: 1, Channels: map[string]catalogEntry{}}
	if info := doc.channel("stable", testArtifactPrefix); info != nil {
		t.Fatalf("expected nil for missing channel, got %+v", info)
	}
}

func TestCatalogEntry_UnparseableVersion(t *testing.T) {
	entry := catalogEntry{Version: "not-a-version"}
	if info := entry.toLatestInfo(testArtifactPrefix); info != nil {
		t.Fatalf("expected nil for unparseable version, got %+v", info)
	}
}

// --- H1: artifact URL pinned to the official repo -------------------------

func TestToLatestInfo_ForeignArtifactHost(t *testing.T) {
	entry := catalogEntry{
		Version:            "v1.0.0",
		WindowsArtifactURL: "https://github.com/kotu/varyaone/releases/download/v1.0.0/varyaone.zip",
		WindowsSHA256:      strings.Repeat("a", 64),
	}
	info := entry.toLatestInfo(testArtifactPrefix)
	if info == nil || info.Version != "v1.0.0" {
		t.Fatalf("expected version to survive, got %+v", info)
	}
	if info.WindowsArtifactURL != "" || info.WindowsSHA256 != "" {
		t.Fatalf("expected artifact fields dropped for a foreign repo, got %+v", info)
	}
}

func TestToLatestInfo_NonHTTPS(t *testing.T) {
	entry := catalogEntry{
		Version:            "v1.0.0",
		WindowsArtifactURL: "http://github.com/alpyxn/varyaone/releases/download/v1.0.0/varyaone.zip",
		WindowsSHA256:      strings.Repeat("a", 64),
	}
	info := entry.toLatestInfo(testArtifactPrefix)
	if info.WindowsArtifactURL != "" {
		t.Fatalf("expected http artifact url dropped, got %+v", info)
	}
}

func TestToLatestInfo_MalformedSHA(t *testing.T) {
	for _, sha := range []string{
		strings.Repeat("a", 63), // too short
		strings.Repeat("a", 65), // too long
		strings.Repeat("g", 64), // not hex
		"",
	} {
		entry := catalogEntry{
			Version:            "v1.0.0",
			WindowsArtifactURL: testArtifactPrefix + "v1.0.0/varyaone.zip",
			WindowsSHA256:      sha,
		}
		info := entry.toLatestInfo(testArtifactPrefix)
		if info.WindowsArtifactURL != "" {
			t.Fatalf("sha %q: expected artifact url dropped, got %+v", sha, info)
		}
	}
}

func TestToLatestInfo_SHACaseNormalized(t *testing.T) {
	entry := catalogEntry{
		Version:            "v1.0.0",
		WindowsArtifactURL: testArtifactPrefix + "v1.0.0/varyaone.zip",
		WindowsSHA256:      strings.ToUpper(strings.Repeat("ab", 32)),
	}
	info := entry.toLatestInfo(testArtifactPrefix)
	if info.WindowsSHA256 != strings.Repeat("ab", 32) {
		t.Fatalf("expected lowercased sha, got %q", info.WindowsSHA256)
	}
}

// --- H2: network hardening -------------------------------------------------

func TestFetchCatalog_OversizedBody(t *testing.T) {
	huge := `{"schema_version": 1, "channels": {}, "padding": "` + strings.Repeat("x", maxCatalogBytes+1) + `"}`
	srv := jsonServer(t, huge)
	_, err := fetchCatalog(context.Background(), srv.Client(), []string{srv.URL}, "test")
	if err == nil {
		t.Fatal("expected error for oversized body")
	}
}

func TestFetchCatalog_NonJSONContentType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/html")
		_, _ = w.Write([]byte("<html>maintenance</html>"))
	}))
	t.Cleanup(srv.Close)
	_, err := fetchCatalog(context.Background(), srv.Client(), []string{srv.URL}, "test")
	if err == nil {
		t.Fatal("expected error for html response")
	}
}

func TestFetchCatalog_RefusesNonHTTPSRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.invalid/latest.json", http.StatusFound)
	}))
	t.Cleanup(target.Close)
	_, err := fetchCatalog(context.Background(), target.Client(), []string{target.URL}, "test")
	if err == nil {
		t.Fatal("expected error for non-https redirect")
	}
}

func TestFetchCatalog_SendsNoCacheHeaders(t *testing.T) {
	var gotCacheControl, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCacheControl = r.Header.Get("cache-control")
		gotQuery = r.URL.RawQuery
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"schema_version": 1, "channels": {}}`))
	}))
	t.Cleanup(srv.Close)
	if _, err := fetchCatalog(context.Background(), srv.Client(), []string{srv.URL}, "test"); err != nil {
		t.Fatalf("fetchCatalog: %v", err)
	}
	if gotCacheControl != "no-cache" {
		t.Fatalf("cache-control = %q, want no-cache", gotCacheControl)
	}
	if !strings.Contains(gotQuery, "_=") {
		t.Fatalf("query = %q, want a cache-buster", gotQuery)
	}
}

// --- H4: yanked releases -----------------------------------------------

func TestCatalogDoc_IsYanked(t *testing.T) {
	doc := &catalogDoc{SchemaVersion: 1, Yanked: []string{"v1.0.0"}}
	if !doc.isYanked("v1.0.0") {
		t.Fatal("expected v1.0.0 to be yanked")
	}
	if doc.isYanked("v1.0.1") {
		t.Fatal("v1.0.1 should not be yanked")
	}
}

func TestCatalogDoc_ChannelSkipsYankedVersion(t *testing.T) {
	doc := &catalogDoc{
		SchemaVersion: 1,
		Channels:      map[string]catalogEntry{"stable": {Version: "v1.0.0"}},
		Yanked:        []string{"v1.0.0"},
	}
	if info := doc.channel("stable", testArtifactPrefix); info != nil {
		t.Fatalf("expected nil for a yanked channel entry, got %+v", info)
	}
}

// --- H8: multiple catalog URLs, first-success wins -------------------------

func TestFetchCatalog_FallsBackToSecondURL(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(bad.Close)
	good := jsonServer(t, `{"schema_version": 1, "channels": {"stable": {"version": "v2.0.0"}}}`)

	doc, err := fetchCatalog(context.Background(), good.Client(), []string{bad.URL, good.URL}, "test")
	if err != nil {
		t.Fatalf("fetchCatalog: %v", err)
	}
	if info := doc.channel("stable", testArtifactPrefix); info == nil || info.Version != "v2.0.0" {
		t.Fatalf("expected fallback to succeed, got %+v", info)
	}
}

func TestFetchCatalog_AllSourcesFail(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(bad.Close)
	_, err := fetchCatalog(context.Background(), bad.Client(), []string{bad.URL, bad.URL}, "test")
	if err == nil {
		t.Fatal("expected error when every catalog source fails")
	}
}

func TestFetchCatalog_NoURLsConfigured(t *testing.T) {
	_, err := fetchCatalog(context.Background(), http.DefaultClient, nil, "test")
	if err == nil {
		t.Fatal("expected error with no catalog URLs")
	}
}

// --- notesFor ---------------------------------------------------------

func TestCatalogDoc_NotesForChannelAndHistory(t *testing.T) {
	doc := &catalogDoc{
		SchemaVersion: 1,
		Channels:      map[string]catalogEntry{"stable": {Version: "v2.0.0", NotesMD: "current"}},
		History:       []catalogEntry{{Version: "v1.9.0", NotesMD: "older"}},
	}
	if got := doc.notesFor("v2.0.0"); got != "current" {
		t.Fatalf("notesFor(v2.0.0) = %q", got)
	}
	if got := doc.notesFor("v1.9.0"); got != "older" {
		t.Fatalf("notesFor(v1.9.0) = %q", got)
	}
	if got := doc.notesFor("v0.0.1"); got != "" {
		t.Fatalf("notesFor(unknown) = %q, want empty", got)
	}
}
