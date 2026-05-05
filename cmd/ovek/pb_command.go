package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/massivemoose/ovek/internal/brainapi"
	"github.com/massivemoose/ovek/internal/cli/client"
	"github.com/massivemoose/ovek/internal/cli/command"
	"github.com/massivemoose/ovek/internal/cli/config"
	"github.com/massivemoose/ovek/internal/cli/output"
)

const defaultPocketBaseTunnelListen = "127.0.0.1:8090"

type pbCommand struct {
	stdout  io.Writer
	config  *config.Store
	prompts prompter
}

func newPBCommand(stdout io.Writer, store *config.Store, prompts prompter) command.Command {
	return &pbCommand{
		stdout:  stdout,
		config:  store,
		prompts: prompts,
	}
}

func (cmd *pbCommand) Name() string { return "pb" }

func (cmd *pbCommand) Summary() string { return "Manage a project's PocketBase sidecar" }

func (cmd *pbCommand) Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return command.ErrUsage
	}

	brainClient, _, err := loadConfiguredClient(cmd.config, "")
	if err != nil {
		return err
	}

	switch args[0] {
	case "init":
		return cmd.runInit(ctx, brainClient, args[1:])
	case "status":
		return cmd.runStatus(ctx, brainClient, args[1:])
	case "tunnel":
		return cmd.runTunnel(ctx, brainClient, args[1:])
	default:
		return fmt.Errorf("unknown pb command %q", args[0])
	}
}

func (cmd *pbCommand) Usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "Usage:\n  ovek pb init <project> [--email <email>] [--app-secrets]\n  ovek pb status <project>\n  ovek pb tunnel <project> [--listen 127.0.0.1:8090]\n")
}

func (cmd *pbCommand) runInit(ctx context.Context, brainClient *client.Client, args []string) error {
	projectName, email, appSecrets, err := parsePocketBaseInitArgs(args)
	if err != nil {
		return err
	}

	status, err := runPocketBaseInitMutation(ctx, brainClient, cmd.prompts, func() (brainapi.ProjectPocketBaseStatus, error) {
		return brainClient.InitProjectPocketBase(ctx, projectName, brainapi.InitProjectPocketBaseRequest{
			Email:      email,
			AppSecrets: appSecrets,
		})
	})
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintln(cmd.stdout, "PocketBase initialized.")
	writePocketBaseStatus(cmd.stdout, status)
	if appSecrets {
		_, _ = fmt.Fprintln(cmd.stdout, "Environment updated. Run 'ovek run <project> <capsule-ref>' to apply changes.")
	}
	return nil
}

func (cmd *pbCommand) runStatus(ctx context.Context, brainClient *client.Client, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("ovek pb status requires <project>")
	}

	status, err := brainClient.GetProjectPocketBase(ctx, args[0])
	if err != nil {
		return err
	}

	writePocketBaseStatus(cmd.stdout, status)
	return nil
}

func (cmd *pbCommand) runTunnel(ctx context.Context, brainClient *client.Client, args []string) error {
	projectName, listenAddress, err := parsePocketBaseTunnelArgs(args)
	if err != nil {
		return err
	}
	if !isLoopbackListenAddress(listenAddress) {
		return fmt.Errorf("ovek pb tunnel --listen must be a loopback host:port")
	}

	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf("listen for PocketBase tunnel: %w", err)
	}
	defer listener.Close()

	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			response, err := brainClient.ProxyProjectPocketBase(r.Context(), projectName, r)
			if err != nil {
				http.Error(w, "PocketBase tunnel request failed", http.StatusBadGateway)
				return
			}
			defer response.Body.Close()

			copyHTTPHeaders(w.Header(), response.Header)
			w.WriteHeader(response.StatusCode)
			_, _ = io.Copy(w, response.Body)
		}),
	}

	go func() {
		<-ctx.Done()
		_ = server.Shutdown(context.Background())
	}()

	dashboardURL := pocketBaseDashboardURL(listener.Addr().String())
	_, _ = fmt.Fprintf(cmd.stdout, "PocketBase tunnel: %s\n", dashboardURL)
	_, _ = fmt.Fprintln(cmd.stdout, "Press Ctrl-C to stop.")

	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func parsePocketBaseInitArgs(args []string) (string, string, bool, error) {
	var projectName string
	var email string
	appSecrets := false

	for index := 0; index < len(args); index++ {
		arg := strings.TrimSpace(args[index])
		switch {
		case arg == "-h" || arg == "--help":
			return "", "", false, command.ErrUsage
		case arg == "--app-secrets":
			appSecrets = true
		case strings.HasPrefix(arg, "--app-secrets="):
			value := strings.TrimPrefix(arg, "--app-secrets=")
			switch strings.ToLower(value) {
			case "true", "1", "yes":
				appSecrets = true
			case "false", "0", "no":
				appSecrets = false
			default:
				return "", "", false, fmt.Errorf("invalid --app-secrets value %q", value)
			}
		case arg == "--email":
			index++
			if index >= len(args) || strings.TrimSpace(args[index]) == "" {
				return "", "", false, fmt.Errorf("ovek pb init --email requires a value")
			}
			email = strings.TrimSpace(args[index])
		case strings.HasPrefix(arg, "--email="):
			email = strings.TrimSpace(strings.TrimPrefix(arg, "--email="))
			if email == "" {
				return "", "", false, fmt.Errorf("ovek pb init --email requires a value")
			}
		case strings.HasPrefix(arg, "-"):
			return "", "", false, fmt.Errorf("unknown pb init flag %q", arg)
		case arg == "":
			return "", "", false, fmt.Errorf("ovek pb init requires <project>")
		default:
			if projectName != "" {
				return "", "", false, fmt.Errorf("ovek pb init accepts one <project>")
			}
			projectName = arg
		}
	}

	if projectName == "" {
		return "", "", false, fmt.Errorf("ovek pb init requires <project>")
	}
	return projectName, email, appSecrets, nil
}

func parsePocketBaseTunnelArgs(args []string) (string, string, error) {
	var projectName string
	listenAddress := defaultPocketBaseTunnelListen

	for index := 0; index < len(args); index++ {
		arg := strings.TrimSpace(args[index])
		switch {
		case arg == "-h" || arg == "--help":
			return "", "", command.ErrUsage
		case arg == "--listen":
			index++
			if index >= len(args) || strings.TrimSpace(args[index]) == "" {
				return "", "", fmt.Errorf("ovek pb tunnel --listen requires a value")
			}
			listenAddress = strings.TrimSpace(args[index])
		case strings.HasPrefix(arg, "--listen="):
			listenAddress = strings.TrimSpace(strings.TrimPrefix(arg, "--listen="))
			if listenAddress == "" {
				return "", "", fmt.Errorf("ovek pb tunnel --listen requires a value")
			}
		case strings.HasPrefix(arg, "-"):
			return "", "", fmt.Errorf("unknown pb tunnel flag %q", arg)
		case arg == "":
			return "", "", fmt.Errorf("ovek pb tunnel requires <project>")
		default:
			if projectName != "" {
				return "", "", fmt.Errorf("ovek pb tunnel accepts one <project>")
			}
			projectName = arg
		}
	}

	if projectName == "" {
		return "", "", fmt.Errorf("ovek pb tunnel requires <project>")
	}
	return projectName, listenAddress, nil
}

func runPocketBaseInitMutation(ctx context.Context, brainClient *client.Client, prompts prompter, mutate func() (brainapi.ProjectPocketBaseStatus, error)) (brainapi.ProjectPocketBaseStatus, error) {
	status, err := mutate()
	if err == nil {
		return status, nil
	}

	var apiErr *client.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "reauth_required" {
		return brainapi.ProjectPocketBaseStatus{}, err
	}

	password, promptErr := prompts.PromptPassword("Password: ")
	if promptErr != nil {
		return brainapi.ProjectPocketBaseStatus{}, promptErr
	}
	if password == "" {
		return brainapi.ProjectPocketBaseStatus{}, fmt.Errorf("password is required")
	}

	reauthResponse, reauthErr := brainClient.Reauth(ctx, password)
	if reauthErr != nil {
		return brainapi.ProjectPocketBaseStatus{}, fmt.Errorf("reauthenticate: %w", reauthErr)
	}
	brainClient.SetReauthToken(reauthResponse.ReauthToken)

	return mutate()
}

func writePocketBaseStatus(w io.Writer, status brainapi.ProjectPocketBaseStatus) {
	output.WriteSection(w, "PocketBase")
	pairs := [][2]string{
		{"Project", status.ProjectName},
		{"Container", status.ContainerName},
		{"Running", boolText(status.Running)},
		{"Initialized", boolText(status.Initialized)},
		{"Superuser Email", pointerOrDash(status.SuperuserEmail)},
		{"App Secrets", boolText(status.AppSecretsConfigured)},
		{"Updated", valueOrDash(status.UpdatedAt)},
	}
	if status.AppSecretsRevisionID != "" {
		pairs = append(pairs, [2]string{"Revision", status.AppSecretsRevisionID})
	}
	output.WriteKeyValues(w, pairs)
}

func isLoopbackListenAddress(address string) bool {
	host, port, err := net.SplitHostPort(strings.TrimSpace(address))
	if err != nil || strings.TrimSpace(port) == "" || strings.TrimSpace(host) == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func pocketBaseDashboardURL(address string) string {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "http://" + address + "/_/"
	}
	if host == "::1" {
		host = "[::1]"
	}
	return "http://" + host + ":" + port + "/_/"
}

func copyHTTPHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func boolText(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func pointerOrDash(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "-"
	}
	return *value
}
