package main

import (
	"errors"
	"os"
	"strings"
)

const defaultDataDir = "/var/lib/alces"
const defaultBuildKitHost = "tcp://buildkitd:1234"

type config struct {
	BrainAPIKey  string
	DataDir      string
	BuildKitHost string
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

	return config{
		BrainAPIKey:  apiKey,
		DataDir:      defaultDataDir,
		BuildKitHost: buildKitHost,
	}, nil
}
