package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const jobLogsDirName = "job-logs"
const (
	railpackPlanDirName  = "railpack-plan"
	railpackPlanFileName = "railpack-plan.json"
	railpackInfoFileName = "railpack-info.json"
)

type buildProcessor struct {
	dataDir                  string
	buildKitHost             string
	buildRegistryPublishHost string
	runtimeRegistryHost      string
	railpackFrontendImage    string
	registryInsecure         bool
	runner                   commandRunner
	configStore              projectConfigStore
}

type commandSpec struct {
	Name   string
	Args   []string
	Env    []string
	Stdout io.Writer
	Stderr io.Writer
}

type commandRunner interface {
	Run(ctx context.Context, command commandSpec) error
}

type systemCommandRunner struct{}

func newBuildProcessor(
	dataDir string,
	buildKitHost string,
	buildRegistryPublishHost string,
	runtimeRegistryHost string,
	railpackFrontendImage string,
	registryInsecure bool,
	runner commandRunner,
	stores ...projectConfigStore,
) buildProcessor {
	if runner == nil {
		runner = systemCommandRunner{}
	}
	var configStore projectConfigStore
	if len(stores) > 0 {
		configStore = stores[0]
	}

	return buildProcessor{
		dataDir:                  dataDir,
		buildKitHost:             buildKitHost,
		buildRegistryPublishHost: buildRegistryPublishHost,
		runtimeRegistryHost:      runtimeRegistryHost,
		railpackFrontendImage:    railpackFrontendImage,
		registryInsecure:         registryInsecure,
		runner:                   runner,
		configStore:              configStore,
	}
}

func (processor buildProcessor) Process(ctx context.Context, job job) (result deploymentResult, err error) {
	result = deploymentResult{
		LogPath:  jobLogPath(processor.dataDir, job.ID),
		ImageRef: jobImageRef(job, processor.runtimeRegistryHost),
	}

	logFile, err := processor.createLogFile(result.LogPath)
	if err != nil {
		return deploymentResult{}, fmt.Errorf("create build log file: %w", err)
	}
	defer logFile.Close()
	scrubber, err := processor.configStore.SecretScrubberForJob(ctx, job)
	if err != nil {
		return result, fmt.Errorf("load project config for log redaction: %w", err)
	}
	logWriter := scrubber.Writer(logFile)
	defer func() {
		if flushErr := logWriter.Flush(); flushErr != nil {
			err = errors.Join(err, fmt.Errorf("flush build log: %w", flushErr))
		}
	}()
	result.LogScrubber = scrubber

	workspace, err := os.MkdirTemp("", "ovek-build-"+job.ID+"-")
	if err != nil {
		return result, fmt.Errorf("create build workspace: %w", err)
	}
	defer os.RemoveAll(workspace)

	if err := writeBuildLifecycleLine(logWriter, "cloning source"); err != nil {
		return result, err
	}
	if err := processor.runCommand(
		ctx,
		commandSpec{
			Name:   "git",
			Args:   []string{"clone", "--depth", "1", job.RepoURL, workspace},
			Env:    nil,
			Stdout: logWriter,
			Stderr: logWriter,
		},
	); err != nil {
		return result, fmt.Errorf("git clone: %w", err)
	}
	if err := writeBuildLifecycleLine(logWriter, "source cloned"); err != nil {
		return result, err
	}

	planDir := filepath.Join(workspace, railpackPlanDirName)
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		return result, fmt.Errorf("create railpack plan directory: %w", err)
	}

	planPath := filepath.Join(planDir, railpackPlanFileName)
	infoPath := filepath.Join(planDir, railpackInfoFileName)
	if err := writeBuildLifecycleLine(logWriter, "planning build with Railpack"); err != nil {
		return result, err
	}
	if err := processor.runCommand(
		ctx,
		commandSpec{
			Name:   "railpack",
			Args:   []string{"prepare", workspace, "--plan-out", planPath, "--info-out", infoPath, "--hide-pretty-plan"},
			Env:    processor.commandEnv(),
			Stdout: logWriter,
			Stderr: logWriter,
		},
	); err != nil {
		return result, fmt.Errorf("railpack prepare: %w", err)
	}
	if err := writeBuildLifecycleLine(logWriter, "build plan prepared"); err != nil {
		return result, err
	}

	if err := writeBuildLifecycleLine(logWriter, "building image with BuildKit; first runs may pull large base images"); err != nil {
		return result, err
	}
	if err := processor.runCommand(
		ctx,
		commandSpec{
			Name:   "buildctl",
			Args:   processor.buildctlBuildArgs(workspace, planDir, job),
			Env:    nil,
			Stdout: logWriter,
			Stderr: logWriter,
		},
	); err != nil {
		return result, fmt.Errorf("buildctl build: %w", err)
	}
	if err := writeBuildLifecycleLine(logWriter, "image built and pushed"); err != nil {
		return result, err
	}

	return result, nil
}

func (processor buildProcessor) createLogFile(logPath string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, err
	}

	return os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
}

func (processor buildProcessor) runCommand(ctx context.Context, command commandSpec) error {
	if _, err := fmt.Fprintf(command.Stdout, "$ %s %s\n", command.Name, strings.Join(command.Args, " ")); err != nil {
		return fmt.Errorf("write command to log: %w", err)
	}

	return processor.runner.Run(ctx, command)
}

func writeBuildLifecycleLine(writer io.Writer, message string) error {
	if _, err := fmt.Fprintf(writer, "lifecycle: %s\n", message); err != nil {
		return fmt.Errorf("write build lifecycle log: %w", err)
	}

	return nil
}

func (systemCommandRunner) Run(ctx context.Context, command commandSpec) error {
	cmd := exec.CommandContext(ctx, command.Name, command.Args...)
	if len(command.Env) > 0 {
		cmd.Env = append(os.Environ(), command.Env...)
	}
	cmd.Stdout = command.Stdout
	cmd.Stderr = command.Stderr

	return cmd.Run()
}

func (processor buildProcessor) commandEnv() []string {
	if processor.buildKitHost == "" {
		return nil
	}

	return []string{"BUILDKIT_HOST=" + processor.buildKitHost}
}

func jobLogPath(dataDir string, jobID string) string {
	return filepath.Join(dataDir, jobLogsDirName, jobID+".log")
}

func (processor buildProcessor) buildctlBuildArgs(workspace string, planDir string, job job) []string {
	args := make([]string, 0, 13)
	if processor.buildKitHost != "" {
		args = append(args, "--addr", processor.buildKitHost)
	}

	args = append(
		args,
		"build",
		"--progress=plain",
		"--local", "context="+workspace,
		"--local", "dockerfile="+planDir,
		"--frontend=gateway.v0",
		"--opt", "source="+processor.railpackFrontendImage,
		"--output", buildctlImageOutput(jobImageRef(job, processor.buildRegistryPublishHost), processor.registryInsecure),
	)

	return args
}

func jobImageName(job job) string {
	return "ovek-" + job.ProjectName + ":" + job.ID
}

func jobImageRef(job job, registryHost string) string {
	imageName := jobImageName(job)
	registryHost = strings.TrimSpace(registryHost)
	if registryHost == "" {
		return imageName
	}

	return strings.TrimRight(registryHost, "/") + "/" + imageName
}

func buildctlImageOutput(imageRef string, insecure bool) string {
	output := "type=image,name=" + imageRef + ",push=true"
	if insecure {
		output += ",registry.insecure=true"
	}

	return output
}
