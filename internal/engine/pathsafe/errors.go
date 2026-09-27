package pathsafe

import "fmt"

// Error describes a rejected path.
type Error struct {
	Path   string
	Reason string
}

func (e *Error) Error() string {
	return fmt.Sprintf("unsafe path %q: %s", e.Path, e.Reason)
}
