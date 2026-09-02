package update

import (
	"regexp"
	"strconv"
)

var semverRE = regexp.MustCompile(`^v?(\d{1,6})\.(\d{1,6})\.(\d{1,6})(?:-[0-9A-Za-z.-]{1,40})?$`)

type semver struct {
	major, minor, patch int
	valid               bool
}

// parseSemver accepts "v1.2.3" or "1.2.3" (optionally with a pre-release
// suffix, which is ignored for ordering). Anything else — "dev", a bare commit
// sha, an empty string — parses as invalid.
func parseSemver(v string) semver {
	m := semverRE.FindStringSubmatch(v)
	if m == nil {
		return semver{}
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	return semver{major: major, minor: minor, patch: patch, valid: true}
}

// compareVersions returns -1 when a is older than b, 0 when equal, 1 when
// newer. An unparseable version is treated as the oldest possible: a "dev" or
// sha build is always considered behind any real tagged release, so an update
// is always offered. Two unparseable versions compare equal.
func compareVersions(a, b string) int {
	pa, pb := parseSemver(a), parseSemver(b)
	switch {
	case !pa.valid && !pb.valid:
		return 0
	case !pa.valid:
		return -1
	case !pb.valid:
		return 1
	}
	for _, d := range [3][2]int{
		{pa.major, pb.major},
		{pa.minor, pb.minor},
		{pa.patch, pb.patch},
	} {
		if d[0] != d[1] {
			if d[0] < d[1] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// isSemver reports whether v is a well-formed version string.
func isSemver(v string) bool { return parseSemver(v).valid }
