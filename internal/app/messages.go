package app

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Knurobroddy/crackers-tui/internal/config"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/hooks"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
)

const maxLeftoversInMessage = 8

// messageRules map known errors to player-facing text, most specific first.
var messageRules = []func(error) (string, bool){
	nothingToRemoveMessage,
	updateRequiredMessage,
	invalidPackMessage,
	permissionMessage,
	prefixNotFoundMessage,
	leftoversMessage,
	removeIncompleteMessage,
	previousHooksUndoneMessage,
	rollbackIncompleteMessage,
}

// bareSentinels carries no detail beyond its advice text, so appending
// "Details: <message>" would only repeat it verbatim.
var bareSentinels = []error{
	engine.ErrNothingToRemove,
	hooks.ErrPrefixNotFound,
	engine.ErrRemoveIncomplete,
	engine.ErrPreviousHooksUndone,
	engine.ErrRollbackIncomplete,
}

// UserMessage turns an error from any layer into text for the player: the
// advice for every known cause, followed by the technical detail, unless err
// is exactly a bare sentinel whose detail would only repeat that advice.
func UserMessage(err error) string {
	if err == nil {
		return ""
	}
	var advice []string
	for _, rule := range messageRules {
		if text, ok := rule(err); ok {
			advice = append(advice, text)
		}
	}
	if len(advice) == 0 {
		return capitalize(err.Error())
	}
	if isBareSentinel(err) {
		return strings.Join(advice, "\n\n")
	}
	return strings.Join(advice, "\n\n") + "\n\nDetails: " + capitalize(err.Error())
}

// ListPaths formats up to limit paths, one per line; folders are marked.
func ListPaths(paths []string, limit int) string {
	var b strings.Builder
	for i, path := range paths {
		if i == limit {
			fmt.Fprintf(&b, "  … and %d more", len(paths)-limit)
			break
		}
		if strings.HasSuffix(path, "/") {
			path += " (folder)"
		}
		fmt.Fprintf(&b, "  %s\n", path)
	}
	return strings.TrimRight(b.String(), "\n")
}

func isBareSentinel(err error) bool {
	for _, sentinel := range bareSentinels {
		//nolint:errorlint // identity on purpose: a wrapped or joined sentinel carries extra context and keeps its Details line
		if err == sentinel {
			return true
		}
	}
	return false
}

func nothingToRemoveMessage(err error) (string, bool) {
	return "Nothing to remove: no pack is installed for this game.", errors.Is(err, engine.ErrNothingToRemove)
}

func updateRequiredMessage(err error) (string, bool) {
	var updateErr *remote.UpdateRequiredError
	if !errors.As(err, &updateErr) {
		return "", false
	}
	return fmt.Sprintf("Please update %s (%s: %s).", config.AppName, updateErr.Doc, updateErr.Reason), true
}

func invalidPackMessage(err error) (string, bool) {
	return "This modpack is broken. Please tell the pack's author.", errors.Is(err, engine.ErrInvalidPack)
}

func permissionMessage(err error) (string, bool) {
	var permErr *engine.PermissionError
	if !errors.As(err, &permErr) {
		return "", false
	}
	return fmt.Sprintf("Permission denied writing to %s. On Windows, try running %s as administrator.", permErr.Path, config.AppName), true
}

func prefixNotFoundMessage(err error) (string, bool) {
	return "The Proton prefix for this game was not found. Launch the game once via Steam (Proton), close it, then retry.",
		errors.Is(err, hooks.ErrPrefixNotFound)
}

func leftoversMessage(err error) (string, bool) {
	var leftoversErr *engine.LeftoversError
	if !errors.As(err, &leftoversErr) {
		return "", false
	}
	return fmt.Sprintf("Leftover mod files already exist in %s:\n%s", leftoversErr.Root, ListPaths(leftoversErr.Paths, maxLeftoversInMessage)), true
}

func removeIncompleteMessage(err error) (string, bool) {
	return "Some files could not be removed. The pack is still recorded as installed, so you can retry.",
		errors.Is(err, engine.ErrRemoveIncomplete)
}

func previousHooksUndoneMessage(err error) (string, bool) {
	return "The previous pack's files were put back, but its game settings were already undone; reinstall it to fix that.",
		errors.Is(err, engine.ErrPreviousHooksUndone)
}

func rollbackIncompleteMessage(err error) (string, bool) {
	return "Some changes could not be undone after the failure. Check the game folder, or reinstall the pack.",
		errors.Is(err, engine.ErrRollbackIncomplete)
}

func capitalize(s string) string {
	if s == "" {
		return ""
	}
	first, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(first)) + s[size:]
}
