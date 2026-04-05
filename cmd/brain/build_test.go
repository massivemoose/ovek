package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildProcessorRunsGitCloneAndRailpackBuild(t *testing.T) {
	dataDir := t.TempDir()
	runner := &recordingCommandRunner{}
	processor := newBuildProcessor(dataDir, "tcp://buildkitd:1234", runner)

	job := job{
		ID:          "job-123",
		ProjectName: "demo-app",
		RepoURL:     "https://example.com/demo.git",
	}

	result, err := processor.Process(context.Background(), job)
	if err != nil {
		t.Fatalf("expected build to succeed, got error: %v", err)
	}

	if result.LogPath != filepath.Join(dataDir, jobLogsDirName, "job-123.log") {
		t.Fatalf("expected log path %q, got %q", filepath.Join(dataDir, jobLogsDirName, "job-123.log"), result.LogPath)
	}
	if result.ImageRef != "alces-demo-app:job-123" {
		t.Fatalf("expected image ref %q, got %q", "alces-demo-app:job-123", result.ImageRef)
	}

	if len(runner.commands) != 2 {
		t.Fatalf("expected 2 commands, got %d", len(runner.commands))
	}
	if runner.commands[0].Name != "git" {
		t.Fatalf("expected first command %q, got %q", "git", runner.commands[0].Name)
	}
	if got := strings.Join(runner.commands[0].Args, " "); got != "clone --depth 1 https://example.com/demo.git "+runner.commands[0].Args[4] {
		t.Fatalf("unexpected git clone args: %q", got)
	}
	if runner.commands[1].Name != "railpack" {
		t.Fatalf("expected second command %q, got %q", "railpack", runner.commands[1].Name)
	}

	workspace := runner.commands[0].Args[4]
	wantRailpackArgs := []string{"build", "--name", "alces-demo-app:job-123", workspace}
	if strings.Join(runner.commands[1].Args, " ") != strings.Join(wantRailpackArgs, " ") {
		t.Fatalf("expected railpack args %q, got %q", strings.Join(wantRailpackArgs, " "), strings.Join(runner.commands[1].Args, " "))
	}
	if len(runner.commands[1].Env) != 1 || runner.commands[1].Env[0] != "BUILDKIT_HOST=tcp://buildkitd:1234" {
		t.Fatalf("expected railpack env %q, got %#v", "BUILDKIT_HOST=tcp://buildkitd:1234", runner.commands[1].Env)
	}

	if _, err := os.Stat(workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected workspace %q to be removed, got error %v", workspace, err)
	}

	logContents, err := os.ReadFile(result.LogPath)
	if err != nil {
		t.Fatalf("expected log file to exist, got error: %v", err)
	}
	if !strings.Contains(string(logContents), "$ git clone --depth 1 https://example.com/demo.git") {
		t.Fatalf("expected git command in log, got %q", string(logContents))
	}
	if !strings.Contains(string(logContents), "$ railpack build --name alces-demo-app:job-123") {
		t.Fatalf("expected railpack command in log, got %q", string(logContents))
	}
}

func TestBuildProcessorCleansUpWorkspaceOnCloneFailure(t *testing.T) {
	dataDir := t.TempDir()
	runner := &recordingCommandRunner{
		runFunc: func(command commandSpec) error {
			if command.Name == "git" {
				return errors.New("clone failed")
			}
			return nil
		},
	}
	processor := newBuildProcessor(dataDir, defaultBuildKitHost, runner)

	result, err := processor.Process(context.Background(), job{
		ID:          "job-456",
		ProjectName: "demo-app",
		RepoURL:     "https://example.com/demo.git",
	})
	if err == nil {
		t.Fatal("expected build to fail")
	}
	if !strings.Contains(err.Error(), "git clone: clone failed") {
		t.Fatalf("expected clone failure, got %v", err)
	}
	if result.LogPath == "" {
		t.Fatal("expected result to include log path")
	}

	workspace := runner.commands[0].Args[4]
	if _, err := os.Stat(workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected workspace %q to be removed, got error %v", workspace, err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("expected only git command to run, got %d commands", len(runner.commands))
	}
}

func TestBuildProcessorReturnsMetadataOnRailpackFailure(t *testing.T) {
	dataDir := t.TempDir()
	runner := &recordingCommandRunner{
		runFunc: func(command commandSpec) error {
			if command.Name == "railpack" {
				return errors.New("railpack failed")
			}
			return nil
		},
	}
	processor := newBuildProcessor(dataDir, defaultBuildKitHost, runner)

	result, err := processor.Process(context.Background(), job{
		ID:          "job-789",
		ProjectName: "demo-app",
		RepoURL:     "https://example.com/demo.git",
	})
	if err == nil {
		t.Fatal("expected build to fail")
	}
	if !strings.Contains(err.Error(), "railpack build: railpack failed") {
		t.Fatalf("expected railpack failure, got %v", err)
	}
	if result.ImageRef != "alces-demo-app:job-789" {
		t.Fatalf("expected image ref %q, got %q", "alces-demo-app:job-789", result.ImageRef)
	}
	if result.LogPath != filepath.Join(dataDir, jobLogsDirName, "job-789.log") {
		t.Fatalf("expected log path %q, got %q", filepath.Join(dataDir, jobLogsDirName, "job-789.log"), result.LogPath)
	}
}

type recordingCommandRunner struct {
	commands []recordedCommand
	runFunc  func(command commandSpec) error
}

type recordedCommand struct {
	Name string
	Args []string
	Env  []string
}

func (runner *recordingCommandRunner) Run(_ context.Context, command commandSpec) error {
	runner.commands = append(runner.commands, recordedCommand{
		Name: command.Name,
		Args: append([]string(nil), command.Args...),
		Env:  append([]string(nil), command.Env...),
	})

	if runner.runFunc != nil {
		return runner.runFunc(command)
	}

	return nil
}
