package update

import (
	"errors"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v1.0.0", "v1.0.0", 0},
		{"1.0.0", "v1.0.0", 0},
		{"v1.2.0", "v1.10.0", -1},
		{"v2.0.0", "v1.9.9", 1},
		{"v1.4.3", "v1.4.2", 1},
		{"v1.4.0-rc1", "v1.4.0", 0}, // pre-release suffix ignored for ordering
		{"dev", "v1.0.0", -1},       // unparseable is always oldest
		{"v1.0.0", "dev", 1},
		{"dev", "abc123", 0}, // two unparseable compare equal
		{"", "v0.0.1", -1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestIsSemver(t *testing.T) {
	for _, v := range []string{"v1.0.0", "1.2.3", "v10.20.30", "v1.0.0-rc.1"} {
		if !isSemver(v) {
			t.Errorf("isSemver(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"", "dev", "v1", "v1.2", "1.2.3.4", "latest", "abc"} {
		if isSemver(v) {
			t.Errorf("isSemver(%q) = true, want false", v)
		}
	}
}

func TestTargetReleaseKeepsQueuedArtifactWhenLatestChanges(t *testing.T) {
	queued := LatestInfo{
		Version:            "v1.5.0",
		WindowsArtifactURL: "https://example.invalid/v1.5.zip",
		WindowsSHA256:      "old-hash",
	}
	latest := LatestInfo{
		Version:            "v1.6.0",
		WindowsArtifactURL: "https://example.invalid/v1.6.zip",
		WindowsSHA256:      "new-hash",
	}
	meta := metaMap{
		keyTargetInfo: mustJSON(queued),
		keyLatest:     mustJSON(latest),
	}

	got, err := targetRelease(meta, "v1.5.0")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != queued.Version || got.WindowsSHA256 != queued.WindowsSHA256 {
		t.Fatalf("targetRelease = %+v, want queued artifact %+v", got, queued)
	}

	if _, err := targetRelease(metaMap{keyLatest: mustJSON(latest)}, "v1.5.0"); !errors.Is(err, ErrTargetMetadata) {
		t.Fatalf("mismatched latest error = %v, want ErrTargetMetadata", err)
	}
}
