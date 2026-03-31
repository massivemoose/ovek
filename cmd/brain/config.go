package main

import (
	"errors"
	"os"
	"strings"
)

const defaultDataDir = "/var/lib/alces"

type config struct {
	BrainAPIKey string
	DataDir     string
}

func loadConfig() (config, error) {
	apiKey := strings.TrimSpace(os.Getenv("BRAIN_API_KEY"))
	if apiKey == "" {
		return config{}, errors.New("BRAIN_API_KEY is required")
	}

	return config{
		BrainAPIKey: apiKey,
		DataDir:     defaultDataDir,
	}, nil
}
