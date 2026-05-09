package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/massivemoose/ovek/internal/cli/client"
)

func runReauthMutation[T any](ctx context.Context, brainClient *client.Client, prompts prompter, mutate func() (T, error)) (T, error) {
	value, err := mutate()
	if err == nil {
		return value, nil
	}

	var zero T
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "reauth_required" {
		return zero, err
	}

	password, promptErr := prompts.PromptPassword("Password: ")
	if promptErr != nil {
		return zero, promptErr
	}
	if password == "" {
		return zero, fmt.Errorf("password is required")
	}

	reauthResponse, reauthErr := brainClient.Reauth(ctx, password)
	if reauthErr != nil {
		return zero, fmt.Errorf("reauthenticate: %w", reauthErr)
	}
	brainClient.SetReauthToken(reauthResponse.ReauthToken)

	return mutate()
}
