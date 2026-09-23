package remote

import (
	"cmp"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Knurobroddy/crackers-tui/internal/config"
)

// UpdateRequiredError means the remote needs a newer app: a schema_version
// above what this build supports, or an unmet min_app_version.
type UpdateRequiredError struct {
	Doc        string // e.g. "games.json"
	MinVersion string // set when min_app_version is not met
	Reason     string
}

func (e *UpdateRequiredError) Error() string {
	return fmt.Sprintf("Please update %s (%s: %s).", config.AppName, e.Doc, e.Reason)
}

// IsUpdateRequired reports whether err is (or wraps) an UpdateRequiredError.
func IsUpdateRequired(err error) bool {
	var u *UpdateRequiredError
	return errors.As(err, &u)
}

// CheckSchema fails if a document's schema_version is newer than supported.
func CheckSchema(doc string, got, supported int) error {
	if got > supported {
		return &UpdateRequiredError{Doc: doc, Reason: fmt.Sprintf("schema_version %d is newer than supported %d", got, supported)}
	}
	if got < 1 {
		return fmt.Errorf("%s: missing or invalid schema_version", doc)
	}
	return nil
}

// CheckMinAppVersion fails if appVersion is older than minVersion. An empty
// minVersion or the dev version always passes.
func CheckMinAppVersion(doc, minVersion, appVersion string) error {
	if minVersion == "" || appVersion == config.DevVersion {
		return nil
	}
	ok, err := VersionAtLeast(appVersion, minVersion)
	if err != nil {
		return fmt.Errorf("%s: %w", doc, err)
	}
	if !ok {
		return &UpdateRequiredError{Doc: doc, MinVersion: minVersion, Reason: fmt.Sprintf("requires version %s or newer", minVersion)}
	}
	return nil
}

// VersionAtLeast reports whether have >= want (semver, optional "v" prefix).
func VersionAtLeast(have, want string) (bool, error) {
	c, err := CompareVersions(have, want)
	return c >= 0, err
}

// CompareVersions compares two semantic versions and returns -1, 0 or 1.
func CompareVersions(a, b string) (int, error) {
	va, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	vb, err := parseVersion(b)
	if err != nil {
		return 0, err
	}
	for i := range 3 {
		if va.core[i] != vb.core[i] {
			return cmp.Compare(va.core[i], vb.core[i]), nil
		}
	}
	return comparePre(va.pre, vb.pre), nil
}

type version struct {
	core [3]int
	pre  []string
}

func parseVersion(s string) (version, error) {
	var v version
	orig := s
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '-'); i >= 0 {
		v.pre = strings.Split(s[i+1:], ".")
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return v, fmt.Errorf("invalid version %q", orig)
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, fmt.Errorf("invalid version %q", orig)
		}
		v.core[i] = n
	}
	return v, nil
}

// comparePre orders pre-release identifiers per semver: a release is greater
// than any pre-release of the same core version.
func comparePre(a, b []string) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1
	case len(b) == 0:
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		na, errA := strconv.Atoi(a[i])
		nb, errB := strconv.Atoi(b[i])
		switch {
		case errA == nil && errB == nil:
			if na != nb {
				return cmp.Compare(na, nb)
			}
		case errA == nil:
			return -1
		case errB == nil:
			return 1
		default:
			if c := strings.Compare(a[i], b[i]); c != 0 {
				return c
			}
		}
	}
	return cmp.Compare(len(a), len(b))
}
