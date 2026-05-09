package main

import (
	"errors"
	"fmt"

	"github.com/massivemoose/ovek/internal/cli/client"
	"github.com/massivemoose/ovek/internal/cli/config"
)

func loadConfiguredClient(store *config.Store, profileName string) (*client.Client, string, error) {
	resolvedProfile, profile, err := store.LoadProfile(profileName)
	if errors.Is(err, config.ErrNotFound) || errors.Is(err, config.ErrProfileNotFound) {
		return nil, "", fmt.Errorf("CLI auth is not configured; run `ovek auth login` or `ovek auth bootstrap` first")
	}
	if err != nil {
		return nil, "", err
	}

	brainClient, err := client.New(profile.Host, profile.APIKey)
	if err != nil {
		return nil, "", err
	}

	return brainClient, resolvedProfile, nil
}
