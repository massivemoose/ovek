package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildProcessorRunsGitCloneAndBuildctl(t *testing.T) {
	dataDir := t.TempDir()
	runner := &recordingCommandRunner{
		runFunc: func(command commandSpec) error {
			if command.Name == "railpack" && len(command.Args) >= 4 && command.Args[0] == "prepare" && command.Args[2] == "--plan-out" {
				return os.WriteFile(command.Args[3], []byte("{}"), 0o644)
			}
			return nil
		},
	}
	processor := newBuildProcessor(
		dataDir,
		"docker-container://buildkit",
		"host.docker.internal:5001",
		"localhost:5001",
		"ghcr.io/railwayapp/railpack-frontend",
		true,
		runner,
	)

	currentJob := job{
		ID:          "job-123",
		ProjectName: "demo-app",
		RepoURL:     "https://example.com/demo.git",
	}

	result, err := processor.Process(context.Background(), currentJob)
	if err != nil {
		t.Fatalf("expected build to succeed, got error: %v", err)
	}

	if result.LogPath != filepath.Join(dataDir, jobLogsDirName, "job-123.log") {
		t.Fatalf("expected log path %q, got %q", filepath.Join(dataDir, jobLogsDirName, "job-123.log"), result.LogPath)
	}
	if result.ImageRef != "localhost:5001/ovek-demo-app:job-123" {
		t.Fatalf("expected image ref %q, got %q", "localhost:5001/ovek-demo-app:job-123", result.ImageRef)
	}

	if len(runner.commands) != 3 {
		t.Fatalf("expected 3 commands, got %d", len(runner.commands))
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
	planDir := filepath.Join(workspace, railpackPlanDirName)
	wantPrepareArgs := []string{
		"prepare",
		workspace,
		"--plan-out",
		filepath.Join(planDir, railpackPlanFileName),
		"--info-out",
		filepath.Join(planDir, railpackInfoFileName),
		"--hide-pretty-plan",
	}
	if strings.Join(runner.commands[1].Args, " ") != strings.Join(wantPrepareArgs, " ") {
		t.Fatalf("expected railpack prepare args %q, got %q", strings.Join(wantPrepareArgs, " "), strings.Join(runner.commands[1].Args, " "))
	}
	if len(runner.commands[1].Env) != 1 || runner.commands[1].Env[0] != "BUILDKIT_HOST=docker-container://buildkit" {
		t.Fatalf("expected railpack env %q, got %#v", "BUILDKIT_HOST=docker-container://buildkit", runner.commands[1].Env)
	}

	if runner.commands[2].Name != "buildctl" {
		t.Fatalf("expected third command %q, got %q", "buildctl", runner.commands[2].Name)
	}
	buildArgs := strings.Join(runner.commands[2].Args, " ")
	for _, want := range []string{
		"--addr docker-container://buildkit",
		"build",
		"--progress=plain",
		"--local context=" + workspace,
		"--local dockerfile=" + planDir,
		"--frontend=gateway.v0",
		"--opt source=ghcr.io/railwayapp/railpack-frontend",
		"--output type=image,name=host.docker.internal:5001/ovek-demo-app:job-123,push=true,registry.insecure=true",
	} {
		if !strings.Contains(buildArgs, want) {
			t.Fatalf("expected buildctl args to contain %q, got %q", want, buildArgs)
		}
	}
	if len(runner.commands[2].Env) != 0 {
		t.Fatalf("expected buildctl env to be empty, got %#v", runner.commands[2].Env)
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
	if !strings.Contains(string(logContents), "$ railpack prepare "+workspace+" --plan-out "+filepath.Join(planDir, railpackPlanFileName)+" --info-out "+filepath.Join(planDir, railpackInfoFileName)+" --hide-pretty-plan") {
		t.Fatalf("expected railpack prepare command in log, got %q", string(logContents))
	}
	if !strings.Contains(string(logContents), "$ buildctl --addr docker-container://buildkit build --progress=plain") {
		t.Fatalf("expected buildctl command in log, got %q", string(logContents))
	}
	for _, fragment := range []string{
		"lifecycle: cloning source",
		"lifecycle: source cloned",
		"lifecycle: planning build with Railpack",
		"lifecycle: build plan prepared",
		"lifecycle: building image with BuildKit; first runs may pull large base images",
		"lifecycle: image built and pushed",
	} {
		if !strings.Contains(string(logContents), fragment) {
			t.Fatalf("expected build lifecycle log to contain %q, got %q", fragment, string(logContents))
		}
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
	processor := newBuildProcessor(
		dataDir,
		defaultBuildKitHost,
		defaultBuildRegistryPublishHost,
		defaultRuntimeRegistryHost,
		defaultRailpackFrontendImage,
		defaultRegistryInsecure,
		runner,
	)

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

func TestBuildProcessorReturnsMetadataOnBuildctlFailure(t *testing.T) {
	dataDir := t.TempDir()
	runner := &recordingCommandRunner{
		runFunc: func(command commandSpec) error {
			if command.Name == "railpack" && len(command.Args) > 0 && command.Args[0] == "prepare" {
				return os.WriteFile(command.Args[3], []byte("{}"), 0o644)
			}
			if command.Name == "buildctl" {
				return errors.New("buildctl failed")
			}
			return nil
		},
	}
	processor := newBuildProcessor(
		dataDir,
		defaultBuildKitHost,
		defaultBuildRegistryPublishHost,
		defaultRuntimeRegistryHost,
		defaultRailpackFrontendImage,
		defaultRegistryInsecure,
		runner,
	)

	result, err := processor.Process(context.Background(), job{
		ID:          "job-789",
		ProjectName: "demo-app",
		RepoURL:     "https://example.com/demo.git",
	})
	if err == nil {
		t.Fatal("expected build to fail")
	}
	if !strings.Contains(err.Error(), "buildctl build: buildctl failed") {
		t.Fatalf("expected buildctl failure, got %v", err)
	}
	if result.ImageRef != "localhost:5001/ovek-demo-app:job-789" {
		t.Fatalf("expected image ref %q, got %q", "localhost:5001/ovek-demo-app:job-789", result.ImageRef)
	}
	if result.LogPath != filepath.Join(dataDir, jobLogsDirName, "job-789.log") {
		t.Fatalf("expected log path %q, got %q", filepath.Join(dataDir, jobLogsDirName, "job-789.log"), result.LogPath)
	}
}

func TestBuildProcessorRedactsConfiguredSecretsFromBuildLogs(t *testing.T) {
	db := newTestDB(t)
	configStore := newTestProjectConfigStore(t, db)
	secretMutation, err := configStore.SetEnvironmentEntry(context.Background(), "demo-app", "PB_SUPERUSER_PASSWORD", "secret-pass", true, "dev")
	if err != nil {
		t.Fatalf("expected secret set to succeed, got error: %v", err)
	}
	dataDir := t.TempDir()
	runner := &recordingCommandRunner{
		runFunc: func(command commandSpec) error {
			_, _ = command.Stdout.Write([]byte("using secret-pa"))
			_, _ = command.Stdout.Write([]byte("ss during command\n"))
			if command.Name == "railpack" && len(command.Args) > 0 && command.Args[0] == "prepare" {
				return os.WriteFile(command.Args[3], []byte("{}"), 0o644)
			}
			return nil
		},
	}
	processor := newBuildProcessor(
		dataDir,
		defaultBuildKitHost,
		defaultBuildRegistryPublishHost,
		defaultRuntimeRegistryHost,
		defaultRailpackFrontendImage,
		defaultRegistryInsecure,
		runner,
		configStore,
	)

	result, err := processor.Process(context.Background(), job{
		ID:               "job-redact",
		ProjectName:      "demo-app",
		RepoURL:          "https://example.com/demo.git",
		ConfigRevisionID: secretMutation.RevisionID,
	})
	if err != nil {
		t.Fatalf("expected build to succeed, got error: %v", err)
	}

	logBytes, err := os.ReadFile(result.LogPath)
	if err != nil {
		t.Fatalf("expected log read to succeed, got error: %v", err)
	}
	logs := string(logBytes)
	if strings.Contains(logs, "secret-pass") {
		t.Fatal("expected logs to redact secret")
	}
	if !strings.Contains(logs, "[redacted]") {
		t.Fatalf("expected logs to contain redaction marker, got %q", logs)
	}
}

func TestJobImageRefUsesOptionalRegistryHost(t *testing.T) {
	currentJob := job{
		ID:          "job-123",
		ProjectName: "demo-app",
	}

	if got := jobImageRef(currentJob, ""); got != "ovek-demo-app:job-123" {
		t.Fatalf("expected image ref %q, got %q", "ovek-demo-app:job-123", got)
	}
	if got := jobImageRef(currentJob, "localhost:5001"); got != "localhost:5001/ovek-demo-app:job-123" {
		t.Fatalf("expected image ref %q, got %q", "localhost:5001/ovek-demo-app:job-123", got)
	}
}

func TestBuildctlImageOutputIncludesInsecureRegistryWhenRequested(t *testing.T) {
	if got := buildctlImageOutput("example.com/demo:job-123", true); got != "type=image,name=example.com/demo:job-123,push=true,registry.insecure=true" {
		t.Fatalf("unexpected insecure buildctl output: %q", got)
	}
	if got := buildctlImageOutput("example.com/demo:job-123", false); got != "type=image,name=example.com/demo:job-123,push=true" {
		t.Fatalf("unexpected secure buildctl output: %q", got)
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
