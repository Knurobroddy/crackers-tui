package app_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Knurobroddy/crackers-tui/internal/app"
	"github.com/Knurobroddy/crackers-tui/internal/engine"
	"github.com/Knurobroddy/crackers-tui/internal/hooks"
	"github.com/Knurobroddy/crackers-tui/internal/remote"
)

func TestUserMessage_knownErrors_containAdvice(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"permission", fmt.Errorf("write x: %w", &engine.PermissionError{Path: `C:\g\x`}), "administrator"},
		{"update required", &remote.UpdateRequiredError{Doc: "games.json", Reason: "schema_version 2"}, "Please update Crackers Modinst"},
		{"no prefix", fmt.Errorf("validate hook: %w", hooks.ErrPrefixNotFound), "Launch the game once via Steam"},
		{"hooks undone", errors.Join(errors.New("x"), engine.ErrPreviousHooksUndone), "reinstall it"},
		{"remove incomplete", errors.Join(engine.ErrRemoveIncomplete, errors.New("x")), "retry"},
		{"nothing to remove", engine.ErrNothingToRemove, "Nothing to remove"},
		{"leftovers", &engine.LeftoversError{Root: "/g", Paths: []string{"BepInEx/"}}, "Leftover mod files"},
		{"rollback incomplete", errors.Join(engine.ErrRollbackIncomplete, errors.New("x")), "could not be undone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := app.UserMessage(tc.err); !strings.Contains(got, tc.want) {
				t.Errorf("UserMessage = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

func TestUserMessage_unknownError_capitalizesTechnicalText(t *testing.T) {
	if got := app.UserMessage(errors.New("open archive x: boom")); got != "Open archive x: boom" {
		t.Errorf("UserMessage = %q", got)
	}
}
