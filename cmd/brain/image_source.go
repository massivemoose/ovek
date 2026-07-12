package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type sourceDispatchProcessor struct {
	repo  deploymentProcessor
	image deploymentProcessor
}

func (processor sourceDispatchProcessor) Process(ctx context.Context, currentJob job) (deploymentResult, error) {
	switch currentJob.SourceType {
	case jobSourceTypeImage:
		if processor.image == nil {
			return deploymentResult{}, errors.New("image source processor is not configured")
		}
		return processor.image.Process(ctx, currentJob)
	case "", jobSourceTypeRepo:
		if processor.repo == nil {
			return deploymentResult{}, errors.New("repo source processor is not configured")
		}
		return processor.repo.Process(ctx, currentJob)
	default:
		return deploymentResult{}, fmt.Errorf("unsupported job source type %q", currentJob.SourceType)
	}
}

type imageProcessor struct {
	dataDir     string
	configStore projectConfigStore
}

func newImageProcessor(dataDir string, stores ...projectConfigStore) imageProcessor {
	var configStore projectConfigStore
	if len(stores) > 0 {
		configStore = stores[0]
	}

	return imageProcessor{
		dataDir:     dataDir,
		configStore: configStore,
	}
}

func (processor imageProcessor) Process(ctx context.Context, currentJob job) (result deploymentResult, err error) {
	imageRef := strings.TrimSpace(currentJob.SourceRef)
	result = deploymentResult{
		LogPath:  jobLogPath(processor.dataDir, currentJob.ID),
		ImageRef: imageRef,
	}
	if imageRef == "" {
		return result, errors.New("image source ref is required")
	}

	logFile, err := createManagedJobLogFile(result.LogPath)
	if err != nil {
		return deploymentResult{}, fmt.Errorf("create image run log file: %w", err)
	}
	defer logFile.Close()

	scrubber, err := processor.configStore.SecretScrubberForJob(ctx, currentJob)
	if err != nil {
		return result, fmt.Errorf("load project config for log redaction: %w", err)
	}
	logWriter := scrubber.Writer(logFile)
	defer func() {
		if flushErr := logWriter.Flush(); flushErr != nil {
			err = errors.Join(err, fmt.Errorf("flush image run log: %w", flushErr))
		}
	}()
	result.LogScrubber = scrubber

	if err := writeBuildLifecycleLine(logWriter, "using prebuilt image "+imageRef); err != nil {
		return result, err
	}

	return result, nil
}

func createManagedJobLogFile(logPath string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, err
	}

	return os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
}
