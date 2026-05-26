package chomp

import (
	"errors"
	"strings"
	"testing"
)

func TestParseAcceptsInterspersedStringAndBoolFlags(t *testing.T) {
	result, err := New("ovek workflow set").
		String("image", Required()).
		String("schedule").
		Positionals(2, 2, "project", "name").
		Parse([]string{
			"workflow-demo", "digest",
			"--image", "ghcr.io/example/digest:latest",
			"--schedule=@hourly",
		})
	if err != nil {
		t.Fatalf("expected parse to succeed, got error: %v", err)
	}

	if got := result.Positional(0); got != "workflow-demo" {
		t.Fatalf("expected first positional, got %q", got)
	}
	if got := result.Positional(1); got != "digest" {
		t.Fatalf("expected second positional, got %q", got)
	}
	if got := result.String("image"); got != "ghcr.io/example/digest:latest" {
		t.Fatalf("expected image flag, got %q", got)
	}
	if got := result.String("schedule"); got != "@hourly" {
		t.Fatalf("expected schedule flag, got %q", got)
	}
}

func TestParseAcceptsFlagsBeforePositionals(t *testing.T) {
	result, err := New("ovek db init").
		String("email").
		Bool("app-secrets").
		Positionals(1, 1, "project").
		Parse([]string{
			"--app-secrets",
			"--email=admin@example.com",
			"demo-app",
		})
	if err != nil {
		t.Fatalf("expected parse to succeed, got error: %v", err)
	}

	if got := result.Positional(0); got != "demo-app" {
		t.Fatalf("expected project positional, got %q", got)
	}
	if got := result.String("email"); got != "admin@example.com" {
		t.Fatalf("expected email flag, got %q", got)
	}
	if !result.Bool("app-secrets") {
		t.Fatalf("expected app-secrets flag to be true")
	}
}

func TestParseAcceptsBoolFlagValues(t *testing.T) {
	for _, args := range [][]string{
		{"--app-secrets=false", "demo-app"},
		{"--app-secrets", "false", "demo-app"},
		{"--app-secrets=0", "demo-app"},
		{"--app-secrets", "no", "demo-app"},
	} {
		result, err := New("ovek db init").
			Bool("app-secrets").
			Positionals(1, 1, "project").
			Parse(args)
		if err != nil {
			t.Fatalf("expected parse of %#v to succeed, got error: %v", args, err)
		}
		if result.Bool("app-secrets") {
			t.Fatalf("expected app-secrets false for args %#v", args)
		}
	}
}

func TestParseStopsFlagParsingAfterDoubleDash(t *testing.T) {
	result, err := New("ovek logs").
		Bool("no-follow").
		Positionals(1, 2, "project", "extra").
		Parse([]string{"--no-follow", "--", "--not-a-flag"})
	if err != nil {
		t.Fatalf("expected parse to succeed, got error: %v", err)
	}

	if !result.Bool("no-follow") {
		t.Fatalf("expected no-follow flag to be true")
	}
	if got := result.Positional(0); got != "--not-a-flag" {
		t.Fatalf("expected post -- token to be positional, got %q", got)
	}
}

func TestParseReturnsHelpError(t *testing.T) {
	_, err := New("ovek status").Parse([]string{"--help"})
	if !errors.Is(err, ErrHelp) {
		t.Fatalf("expected ErrHelp, got %v", err)
	}

	_, err = New("ovek status").Parse([]string{"-h"})
	if !errors.Is(err, ErrHelp) {
		t.Fatalf("expected ErrHelp for short help, got %v", err)
	}
}

func TestParseRejectsUnknownFlag(t *testing.T) {
	_, err := New("ovek workflow set").
		String("image").
		Positionals(2, 2, "project", "name").
		Parse([]string{"workflow-demo", "digest", "--wat"})
	if err == nil || !strings.Contains(err.Error(), `unknown workflow set flag "--wat"`) {
		t.Fatalf("expected unknown flag error, got %v", err)
	}
}

func TestParseRejectsMissingStringValue(t *testing.T) {
	_, err := New("ovek workflow set").
		String("image", Required()).
		Positionals(2, 2, "project", "name").
		Parse([]string{"workflow-demo", "digest", "--image"})
	if err == nil || !strings.Contains(err.Error(), "ovek workflow set --image requires a value") {
		t.Fatalf("expected missing value error, got %v", err)
	}
}

func TestParseRejectsEmptyRequiredStringValue(t *testing.T) {
	_, err := New("ovek workflow set").
		String("image", Required()).
		Positionals(2, 2, "project", "name").
		Parse([]string{"workflow-demo", "digest", "--image="})
	if err == nil || !strings.Contains(err.Error(), "ovek workflow set --image requires a value") {
		t.Fatalf("expected empty required string error, got %v", err)
	}
}

func TestParseUsesLastRepeatedFlagValue(t *testing.T) {
	result, err := New("ovek workflow set").
		String("image", Required()).
		Bool("enabled").
		Positionals(2, 2, "project", "name").
		Parse([]string{
			"--image", "ghcr.io/example/old:latest",
			"--image=ghcr.io/example/new:latest",
			"--enabled=false",
			"--enabled",
			"workflow-demo", "digest",
		})
	if err != nil {
		t.Fatalf("expected parse to succeed, got error: %v", err)
	}

	if got := result.String("image"); got != "ghcr.io/example/new:latest" {
		t.Fatalf("expected last image value to win, got %q", got)
	}
	if !result.Bool("enabled") {
		t.Fatalf("expected last bool value to win")
	}
}

func TestParseTracksLastFlagForMutuallyExclusiveOptions(t *testing.T) {
	result, err := New("ovek workflow logs").
		Bool("follow").
		Bool("no-follow").
		Positionals(2, 2, "project", "run-id").
		Parse([]string{"workflow-demo", "run_123", "--no-follow", "--follow"})
	if err != nil {
		t.Fatalf("expected parse to succeed, got error: %v", err)
	}

	if got := result.LastFlag("follow", "no-follow"); got != "follow" {
		t.Fatalf("expected last follow flag, got %q", got)
	}
}

func TestParseRejectsTooFewAndTooManyPositionals(t *testing.T) {
	_, err := New("ovek workflow set").
		String("image", Required()).
		Positionals(2, 2, "project", "name").
		Parse([]string{"workflow-demo", "--image", "ghcr.io/example/digest:latest"})
	if err == nil || !strings.Contains(err.Error(), "ovek workflow set requires <project> and <name>") {
		t.Fatalf("expected too few positionals error, got %v", err)
	}

	_, err = New("ovek db init").
		Positionals(1, 1, "project").
		Parse([]string{"demo-app", "extra"})
	if err == nil || !strings.Contains(err.Error(), "ovek db init accepts one <project>") {
		t.Fatalf("expected too many positionals error, got %v", err)
	}
}
