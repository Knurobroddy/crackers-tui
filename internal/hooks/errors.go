package hooks

import "errors"

// ErrPrefixNotFound means the game's Proton prefix (user.reg) does not exist
// in any Steam library.
var ErrPrefixNotFound = errors.New("proton prefix not found")
