package remote

import (
	"errors"
	"fmt"
)

// UpdateRequiredError means the remote needs a newer app: a schema_version
// above what this build supports, or an unmet min_app_version.
type UpdateRequiredError struct {
	Doc        string // e.g. "games.json"
	MinVersion string // set when min_app_version is not met
	Reason     string
}

func (e *UpdateRequiredError) Error() string {
	return fmt.Sprintf("app update required: %s: %s", e.Doc, e.Reason)
}

// IsUpdateRequired reports whether err is (or wraps) an UpdateRequiredError.
func IsUpdateRequired(err error) bool {
	var updateRequired *UpdateRequiredError
	return errors.As(err, &updateRequired)
}
