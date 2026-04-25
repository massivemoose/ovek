package command

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRouterDispatchesKnownCommand(t *testing.T) {
	command := &recordingCommand{name: "status", summary: "Show project status"}
	router := NewRouter("ovek", "Ovek CLI", command)

	err := router.Run(context.Background(), []string{"status", "demo-app"})
	if err != nil {
		t.Fatalf("expected dispatch to succeed, got error: %v", err)
	}
	if len(command.args) != 1 || command.args[0] != "demo-app" {
		t.Fatalf("expected forwarded args %q, got %q", []string{"demo-app"}, command.args)
	}
}

func TestRouterReturnsUsageForHelp(t *testing.T) {
	router := NewRouter("ovek", "Ovek CLI")

	err := router.Run(context.Background(), []string{"help"})
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("expected ErrUsage, got %v", err)
	}
}

func TestRouterUsageListsCommands(t *testing.T) {
	router := NewRouter(
		"ovek",
		"Ovek CLI",
		&recordingCommand{name: "status", summary: "Show project status"},
		&recordingCommand{name: "auth", summary: "Manage local auth"},
	)

	var usage strings.Builder
	router.Usage(&usage)

	text := usage.String()
	for _, fragment := range []string{
		"Ovek CLI",
		"Usage:",
		"ovek <command>",
		"auth",
		"status",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("expected usage to contain %q, got %q", fragment, text)
		}
	}
}

type recordingCommand struct {
	name    string
	summary string
	args    []string
	err     error
}

func (command *recordingCommand) Name() string { return command.name }

func (command *recordingCommand) Summary() string { return command.summary }

func (command *recordingCommand) Run(_ context.Context, args []string) error {
	command.args = append([]string(nil), args...)
	return command.err
}

func (command *recordingCommand) Usage(_ io.Writer) {}
