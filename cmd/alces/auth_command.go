package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/massivemoose/alces/internal/brainapi"
	"github.com/massivemoose/alces/internal/cli/client"
	"github.com/massivemoose/alces/internal/cli/command"
	"github.com/massivemoose/alces/internal/cli/config"
	"github.com/massivemoose/alces/internal/cli/output"
)

func newAuthCommand(stdout io.Writer, stderr io.Writer, store *config.Store, prompts prompter) command.Command {
	return command.NewRouter(
		"auth",
		"Manage local Brain authentication.",
		&authBootstrapCommand{stdout: stdout, stderr: stderr, config: store, prompts: prompts},
		&authLoginCommand{stdout: stdout, stderr: stderr, config: store},
		&authStatusCommand{stdout: stdout, config: store},
		&authLogoutCommand{stdout: stdout, config: store},
		&authProfilesCommand{stdout: stdout, config: store},
		&authUseCommand{stdout: stdout, config: store},
	)
}

type authBootstrapCommand struct {
	stdout  io.Writer
	stderr  io.Writer
	config  *config.Store
	prompts prompter
}

func (cmd *authBootstrapCommand) Name() string { return "bootstrap" }

func (cmd *authBootstrapCommand) Summary() string {
	return "Create the first Brain admin and save its API key"
}

func (cmd *authBootstrapCommand) Run(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("alces auth bootstrap", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)

	host := flagSet.String("host", config.DefaultHost, "Brain base URL")
	profileName := flagSet.String("profile", config.DefaultProfile, "Local profile name")
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("alces auth bootstrap does not accept positional arguments")
	}

	username, err := cmd.prompts.Prompt("Username: ")
	if err != nil {
		return err
	}
	password, err := cmd.prompts.PromptPassword("Password: ")
	if err != nil {
		return err
	}
	passwordConfirm, err := cmd.prompts.PromptPassword("Confirm password: ")
	if err != nil {
		return err
	}
	if strings.TrimSpace(username) == "" || strings.TrimSpace(password) == "" {
		return fmt.Errorf("username and password are required")
	}
	if password != passwordConfirm {
		return fmt.Errorf("passwords do not match")
	}

	brainClient, err := client.New(*host, "")
	if err != nil {
		return err
	}

	response, err := brainClient.Bootstrap(ctx, brainapi.BootstrapAuthRequest{
		Username: username,
		Password: password,
	})
	if err != nil {
		return err
	}

	if err := cmd.config.SaveProfile(*profileName, config.Profile{
		Host:   *host,
		APIKey: response.APIKey,
	}, true); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.stdout, "Bootstrapped admin %s\n", response.Username)
	_, _ = fmt.Fprintf(cmd.stdout, "Profile: %s\n", strings.TrimSpace(*profileName))
	_, _ = fmt.Fprintf(cmd.stdout, "Host: %s\n", strings.TrimRight(strings.TrimSpace(*host), "/"))
	return nil
}

func (cmd *authBootstrapCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  alces auth bootstrap --profile <name> [--host <url>]\n")
}

type authLoginCommand struct {
	stdout io.Writer
	stderr io.Writer
	config *config.Store
}

func (cmd *authLoginCommand) Name() string { return "login" }

func (cmd *authLoginCommand) Summary() string { return "Verify and save local Brain auth" }

func (cmd *authLoginCommand) Run(ctx context.Context, args []string) error {
	flagSet := flag.NewFlagSet("alces auth login", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)

	host := flagSet.String("host", config.DefaultHost, "Brain base URL")
	apiKey := flagSet.String("api-key", "", "Brain API key")
	profileName := flagSet.String("profile", config.DefaultProfile, "Local profile name")
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("alces auth login does not accept positional arguments")
	}
	if strings.TrimSpace(*apiKey) == "" {
		return fmt.Errorf("alces auth login requires --api-key")
	}

	brainClient, err := client.New(*host, *apiKey)
	if err != nil {
		return err
	}
	if err := brainClient.Ping(ctx); err != nil {
		return fmt.Errorf("verify credentials: %w", err)
	}

	if err := cmd.config.SaveProfile(*profileName, config.Profile{
		Host:   *host,
		APIKey: *apiKey,
	}, true); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.stdout, "Saved credentials for %s\n", strings.TrimSpace(*profileName))
	_, _ = fmt.Fprintf(cmd.stdout, "Host: %s\n", strings.TrimRight(strings.TrimSpace(*host), "/"))
	return nil
}

func (cmd *authLoginCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  alces auth login --profile <name> [--host <url>] --api-key <key>\n")
}

type authStatusCommand struct {
	stdout io.Writer
	config *config.Store
}

func (cmd *authStatusCommand) Name() string { return "status" }

func (cmd *authStatusCommand) Summary() string { return "Show local auth configuration status" }

func (cmd *authStatusCommand) Run(_ context.Context, args []string) error {
	flagSet := flag.NewFlagSet("alces auth status", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	profileName := flagSet.String("profile", "", "Profile name")
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("alces auth status does not accept positional arguments")
	}

	configPath, err := cmd.config.Path()
	if err != nil {
		return err
	}

	cfg, err := cmd.config.Load()
	if errors.Is(err, config.ErrNotFound) {
		_, _ = fmt.Fprintf(cmd.stdout, "Authenticated: no\n")
		_, _ = fmt.Fprintf(cmd.stdout, "Config: %s\n", configPath)
		return nil
	}
	if err != nil {
		return err
	}

	resolvedProfile := strings.TrimSpace(*profileName)
	if resolvedProfile == "" {
		resolvedProfile = cfg.ActiveProfile
	}
	profile, ok := cfg.Profiles[resolvedProfile]
	if !ok {
		return config.ErrProfileNotFound
	}

	output.WriteKeyValues(cmd.stdout, [][2]string{
		{"Authenticated", "yes"},
		{"Profile", resolvedProfile},
		{"Active Profile", cfg.ActiveProfile},
		{"Host", profile.Host},
		{"Config", configPath},
	})
	return nil
}

func (cmd *authStatusCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  alces auth status [--profile <name>]\n")
}

type authLogoutCommand struct {
	stdout io.Writer
	config *config.Store
}

func (cmd *authLogoutCommand) Name() string { return "logout" }

func (cmd *authLogoutCommand) Summary() string { return "Clear local Brain auth" }

func (cmd *authLogoutCommand) Run(_ context.Context, args []string) error {
	flagSet := flag.NewFlagSet("alces auth logout", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	profileName := flagSet.String("profile", "", "Profile name")
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("alces auth logout does not accept positional arguments")
	}

	if err := cmd.config.RemoveProfile(*profileName); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.stdout, "Cleared local auth.\n")
	return nil
}

func (cmd *authLogoutCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  alces auth logout [--profile <name>]\n")
}

type authProfilesCommand struct {
	stdout io.Writer
	config *config.Store
}

func (cmd *authProfilesCommand) Name() string { return "profiles" }

func (cmd *authProfilesCommand) Summary() string { return "List configured local profiles" }

func (cmd *authProfilesCommand) Run(_ context.Context, args []string) error {
	flagSet := flag.NewFlagSet("alces auth profiles", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("alces auth profiles does not accept positional arguments")
	}

	cfg, err := cmd.config.Load()
	if errors.Is(err, config.ErrNotFound) {
		_, _ = fmt.Fprintln(cmd.stdout, "No profiles configured.")
		return nil
	}
	if err != nil {
		return err
	}

	output.WriteSection(cmd.stdout, "Profiles")
	rows := make([][]string, 0, len(cfg.Profiles))
	for _, name := range orderedProfileNames(cfg) {
		profile := cfg.Profiles[name]
		active := ""
		if name == cfg.ActiveProfile {
			active = "*"
		}
		rows = append(rows, []string{active, name, profile.Host})
	}
	output.WriteTable(cmd.stdout, []string{"", "Profile", "Host"}, rows)
	return nil
}

func (cmd *authProfilesCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  alces auth profiles\n")
}

type authUseCommand struct {
	stdout io.Writer
	config *config.Store
}

func (cmd *authUseCommand) Name() string { return "use" }

func (cmd *authUseCommand) Summary() string { return "Set the active local profile" }

func (cmd *authUseCommand) Run(_ context.Context, args []string) error {
	flagSet := flag.NewFlagSet("alces auth use", flag.ContinueOnError)
	flagSet.SetOutput(io.Discard)
	if err := flagSet.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return command.ErrUsage
		}
		return err
	}
	if flagSet.NArg() != 1 {
		return fmt.Errorf("alces auth use requires exactly one <profile> argument")
	}

	profileName := strings.TrimSpace(flagSet.Arg(0))
	if err := cmd.config.SetActiveProfile(profileName); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.stdout, "Active profile: %s\n", profileName)
	return nil
}

func (cmd *authUseCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  alces auth use <profile>\n")
}

func orderedProfileNames(cfg config.Config) []string {
	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
