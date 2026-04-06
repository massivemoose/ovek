package main

import (
	"errors"
	"os"
	"strings"
)

const defaultDataDir = "/var/lib/alces"
const defaultBuildKitHost = "tcp://buildkitd:1234"
const defaultProjectsHostDataDir = "/var/lib/alces/projects"
const defaultPocketBaseImage = "elestio/pocketbase:latest"

type config struct {
	BrainAPIKey         string
	DataDir             string
	BuildKitHost        string
	ProjectsHostDataDir string
	PocketBaseImage     string
}

func loadConfig() (config, error) {
	apiKey := strings.TrimSpace(os.Getenv("BRAIN_API_KEY"))
	if apiKey == "" {
		return config{}, errors.New("BRAIN_API_KEY is required")
	}

	buildKitHost := strings.TrimSpace(os.Getenv("BUILDKIT_HOST"))
	if buildKitHost == "" {
		buildKitHost = defaultBuildKitHost
	}

	projectsHostDataDir := strings.TrimSpace(os.Getenv("PROJECTS_HOST_DATA_DIR"))
	if projectsHostDataDir == "" {
		projectsHostDataDir = defaultProjectsHostDataDir
	}

	pocketBaseImage := strings.TrimSpace(os.Getenv("POCKETBASE_IMAGE"))
	if pocketBaseImage == "" {
		pocketBaseImage = defaultPocketBaseImage
	}

	return config{
		BrainAPIKey:         apiKey,
		DataDir:             defaultDataDir,
		BuildKitHost:        buildKitHost,
		ProjectsHostDataDir: projectsHostDataDir,
		PocketBaseImage:     pocketBaseImage,
	}, nil
}
