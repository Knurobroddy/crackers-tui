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
		name        string
		err         error
		want        string
		wantDetails bool
	}{
		{"permission", fmt.Errorf("write x: %w", &engine.PermissionError{Path: `C:\g\x`}), "administrator", true},
		{"update required", &remote.UpdateRequiredError{Doc: "games.json", Reason: "schema_version 2"}, "Please update Crackers Modinst", true},
		{"no prefix", fmt.Errorf("validate hook: %w", hooks.ErrPrefixNotFound), "Launch the game once via Steam", true},
		{"hooks undone", errors.Join(errors.New("x"), engine.ErrPreviousHooksUndone), "reinstall it", true},
		{"remove incomplete", errors.Join(engine.ErrRemoveIncomplete, errors.New("x")), "retry", true},
		{"nothing to remove", engine.ErrNothingToRemove, "Nothing to remove", false},
		{"leftovers", &engine.LeftoversError{Root: "/g", Paths: []string{"BepInEx/"}}, "Leftover mod files", true},
		{"rollback incomplete", errors.Join(engine.ErrRollbackIncomplete, errors.New("x")), "could not be undone", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := app.UserMessage(tc.err)
			if !strings.Contains(got, tc.want) {
				t.Errorf("UserMessage = %q, want it to contain %q", got, tc.want)
			}
			if hasDetails := strings.Contains(got, "Details:"); hasDetails != tc.wantDetails {
				t.Errorf("UserMessage = %q, want Details line: %v", got, tc.wantDetails)
			}
		})
	}
}

func TestUserMessage_bareSentinel_omitsDetails(t *testing.T) {
	want := "Nothing to remove: no pack is installed for this game."
	if got := app.UserMessage(engine.ErrNothingToRemove); got != want {
		t.Errorf("UserMessage(ErrNothingToRemove) = %q, want %q", got, want)
	}
}

func TestUserMessage_unknownError_capitalizesTechnicalText(t *testing.T) {
	if got := app.UserMessage(errors.New("open archive x: boom")); got != "Open archive x: boom" {
		t.Errorf("UserMessage = %q", got)
	}
}

func TestUserMessage_nilError_returnsEmpty(t *testing.T) {
	if got := app.UserMessage(nil); got != "" {
		t.Errorf("UserMessage(nil) = %q, want empty", got)
	}
}

func TestUserMessage_emptyMessageError_returnsEmpty(t *testing.T) {
	if got := app.UserMessage(errors.New("")); got != "" {
		t.Errorf("UserMessage = %q, want empty", got)
	}
}
