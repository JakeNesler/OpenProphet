package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Version of the OpenProphet CLI
var Version = "2.0.0-dev"

const defaultAPIUrl = "https://openprophet.io"

// Manifest defines the contract for fetching the appliance manifest.
type Manifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	Image         string `json:"image"`
	DashboardURL  string `json:"dashboardUrl,omitempty"`
}

// Allow mocking of commands in tests.
var execCommand = exec.Command

func getHomeDir() string {
	if home := os.Getenv("OPENPROPHET_HOME"); home != "" {
		return home
	}
	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, ".config", "openprophet")
		}
		return ".openprophet"
	}
	return filepath.Join(userConfigDir, "openprophet")
}

func saveEntitlementKey(key string) error {
	homeDir := getHomeDir()
	if err := ensurePrivateDir(homeDir); err != nil {
		return fmt.Errorf("failed to create home directory: %v", err)
	}
	keyPath := filepath.Join(homeDir, "entitlement.key")
	return writePrivateFile(keyPath, []byte(strings.TrimSpace(key)))
}

func ensurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	return os.Chmod(path, 0700)
}

func writePrivateFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func loadEntitlementKey() (string, error) {
	homeDir := getHomeDir()
	keyPath := filepath.Join(homeDir, "entitlement.key")
	data, err := os.ReadFile(keyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("entitlement key not found, please run install first")
		}
		return "", fmt.Errorf("failed to read entitlement key")
	}
	return strings.TrimSpace(string(data)), nil
}

func validateOrigin(apiURL string) error {
	parsed, err := url.Parse(apiURL)
	if err != nil {
		return fmt.Errorf("invalid API URL")
	}
	if parsed.Scheme == "" || parsed.Hostname() == "" {
		return fmt.Errorf("API URL must specify a scheme")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("API URL must be an origin without credentials, query, or fragment")
	}
	if parsed.Scheme != "https" {
		host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
		isLoopback := host == "localhost" || host == "127.0.0.1" || host == "::1"
		if !isLoopback {
			// Additionally allow plain HTTP only when BOTH are true:
			// 1. OPENPROPHET_ALLOW_INSECURE_LOCAL equals the exact string "1".
			// 2. The parsed hostname ends in .local on a DNS-label boundary.
			allowInsecure := os.Getenv("OPENPROPHET_ALLOW_INSECURE_LOCAL") == "1"
			labels := strings.Split(host, ".")
			isValidLocal := len(labels) >= 2 && labels[len(labels)-1] == "local"
			if isValidLocal {
				for _, label := range labels {
					if label == "" {
						isValidLocal = false
						break
					}
				}
			}
			if !(parsed.Scheme == "http" && allowInsecure && isValidLocal) {
				return fmt.Errorf("non-HTTPS API origins are only allowed for loopback development")
			}
		}
	}
	return nil
}

func validateManifest(m *Manifest) error {
	if m.SchemaVersion != 1 {
		return fmt.Errorf("unsupported manifest schema version")
	}
	image := strings.TrimSpace(m.Image)
	if image == "" {
		return fmt.Errorf("malformed manifest: missing image field")
	}
	if strings.HasSuffix(image, ":latest") || (!strings.Contains(image, "@sha256:") && !strings.Contains(filepath.Base(image), ":")) {
		return fmt.Errorf("malformed manifest: image must use a version tag or digest")
	}
	if strings.ContainsAny(image, "\r\n\t $") {
		return fmt.Errorf("malformed manifest: invalid image reference")
	}
	if m.DashboardURL != "" {
		dashboard, err := url.Parse(m.DashboardURL)
		if err != nil || (dashboard.Scheme != "http" && dashboard.Scheme != "https") || dashboard.Hostname() == "" {
			return fmt.Errorf("malformed manifest: invalid dashboard URL")
		}
		host := dashboard.Hostname()
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			return fmt.Errorf("malformed manifest: dashboard URL must be loopback")
		}
	}
	return nil
}

func fetchManifest(ctx context.Context, apiURL string, key string) (*Manifest, error) {
	if err := validateOrigin(apiURL); err != nil {
		return nil, err
	}

	manifestEndpoint := fmt.Sprintf("%s/api/appliance/manifest", strings.TrimSuffix(apiURL, "/"))
	req, err := http.NewRequestWithContext(ctx, "GET", manifestEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request")
	}

	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch manifest: connection error")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("unauthorized: invalid entitlement key")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to fetch manifest: server returned HTTP %d", resp.StatusCode)
	}

	var m Manifest
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("malformed manifest: invalid JSON")
	}

	if err := validateManifest(&m); err != nil {
		return nil, err
	}

	return &m, nil
}

func saveManifest(m *Manifest) error {
	homeDir := getHomeDir()
	if err := ensurePrivateDir(homeDir); err != nil {
		return fmt.Errorf("failed to create home directory: %v", err)
	}
	manifestPath := filepath.Join(homeDir, "manifest.json")
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to serialize manifest")
	}
	return writePrivateFile(manifestPath, data)
}

func loadSavedDashboardURL() string {
	homeDir := getHomeDir()
	manifestPath := filepath.Join(homeDir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "http://127.0.0.1:3737"
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return "http://127.0.0.1:3737"
	}
	if m.DashboardURL != "" {
		return m.DashboardURL
	}
	return "http://127.0.0.1:3737"
}

const composeTemplate = `services:
  appliance:
    image: "${OPENPROPHET_IMAGE}"
    init: true
    restart: unless-stopped
    security_opt:
      - no-new-privileges:true
    ports:
      - "127.0.0.1:3737:3737"
    volumes:
      - openprophet_data:/app/data
    env_file:
      - .env
    healthcheck:
      test: ["CMD", "curl", "-f", "http://127.0.0.1:3737/api/health"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 10s

volumes:
  openprophet_data:
    name: openprophet_data
`

func writeDockerCompose() error {
	homeDir := getHomeDir()
	if err := ensurePrivateDir(homeDir); err != nil {
		return fmt.Errorf("failed to create home directory: %v", err)
	}
	composePath := filepath.Join(homeDir, "docker-compose.yml")
	return os.WriteFile(composePath, []byte(composeTemplate), 0644)
}

func updateEnvFile(envPath string, image string) error {
	var lines []string
	found := false
	if content, err := os.ReadFile(envPath); err == nil {
		lines = strings.Split(string(content), "\n")
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "OPENPROPHET_IMAGE=") {
				lines[i] = fmt.Sprintf("OPENPROPHET_IMAGE=%s", image)
				found = true
			}
		}
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	if !found {
		lines = append(lines,
			"# OpenProphet Appliance Environment Variables",
			fmt.Sprintf("OPENPROPHET_IMAGE=%s", image),
			"",
			"# OPTIONAL: Local model/news credentials",
			"# ANTHROPIC_API_KEY=your-key",
			"# OPENAI_API_KEY=your-key",
			"# GEMINI_API_KEY=your-key",
		)
	}

	newContent := strings.Join(lines, "\n")
	if !strings.HasSuffix(newContent, "\n") {
		newContent += "\n"
	}
	return writePrivateFile(envPath, []byte(newContent))
}

func checkDockerCompose() error {
	cmd := execCommand("docker", "compose", "version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker compose is not available on this host: %v", err)
	}
	return nil
}

func runDockerComposeCmdInteractive(stdin io.Reader, stdout, stderr io.Writer, args ...string) error {
	fullArgs := append([]string{"compose"}, args...)
	cmd := execCommand("docker", fullArgs...)
	cmd.Dir = getHomeDir()
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func handleInstall(ctx context.Context, keyFlag string, apiURL string, stdout, stderr io.Writer) error {
	if err := checkDockerCompose(); err != nil {
		return err
	}

	key := keyFlag
	if key == "" {
		key = os.Getenv("OPENPROPHET_KEY")
	}
	if key == "" {
		var err error
		key, err = loadEntitlementKey()
		if err != nil {
			return fmt.Errorf("entitlement key is required (specify via --key or OPENPROPHET_KEY env var)")
		}
	}

	m, err := fetchManifest(ctx, apiURL, key)
	if err != nil {
		return err
	}

	if err := saveEntitlementKey(key); err != nil {
		return err
	}

	if err := saveManifest(m); err != nil {
		return err
	}

	if err := writeDockerCompose(); err != nil {
		return err
	}

	homeDir := getHomeDir()
	envPath := filepath.Join(homeDir, ".env")
	if err := updateEnvFile(envPath, m.Image); err != nil {
		return err
	}

	fmt.Fprintln(stdout, "Pulling appliance images...")
	if err := runDockerComposeCmdInteractive(os.Stdin, stdout, stderr, "pull"); err != nil {
		return fmt.Errorf("failed to pull image: %v", err)
	}

	fmt.Fprintln(stdout, "Starting appliance container...")
	if err := runDockerComposeCmdInteractive(os.Stdin, stdout, stderr, "up", "-d"); err != nil {
		return fmt.Errorf("failed to start container: %v", err)
	}

	fmt.Fprintln(stdout, "OpenProphet appliance installed and started successfully.")
	return nil
}

func handleUpdate(ctx context.Context, keyFlag string, apiURL string, stdout, stderr io.Writer) error {
	if err := checkDockerCompose(); err != nil {
		return err
	}

	key := keyFlag
	if key == "" {
		key = os.Getenv("OPENPROPHET_KEY")
	}
	if key == "" {
		var err error
		key, err = loadEntitlementKey()
		if err != nil {
			return fmt.Errorf("entitlement key not found (run install first, or specify via --key/OPENPROPHET_KEY)")
		}
	}

	m, err := fetchManifest(ctx, apiURL, key)
	if err != nil {
		return err
	}

	if err := saveEntitlementKey(key); err != nil {
		return err
	}

	if err := saveManifest(m); err != nil {
		return err
	}

	if err := writeDockerCompose(); err != nil {
		return err
	}

	homeDir := getHomeDir()
	envPath := filepath.Join(homeDir, ".env")
	if err := updateEnvFile(envPath, m.Image); err != nil {
		return err
	}

	fmt.Fprintln(stdout, "Pulling updated images...")
	if err := runDockerComposeCmdInteractive(os.Stdin, stdout, stderr, "pull"); err != nil {
		return fmt.Errorf("failed to pull image: %v", err)
	}

	fmt.Fprintln(stdout, "Recreating appliance container...")
	if err := runDockerComposeCmdInteractive(os.Stdin, stdout, stderr, "up", "-d"); err != nil {
		return fmt.Errorf("failed to start container: %v", err)
	}

	fmt.Fprintln(stdout, "OpenProphet appliance updated successfully.")
	return nil
}

func handleStart(stdout, stderr io.Writer) error {
	return runDockerComposeCmdInteractive(os.Stdin, stdout, stderr, "up", "-d")
}

func handleStop(stdout, stderr io.Writer) error {
	return runDockerComposeCmdInteractive(os.Stdin, stdout, stderr, "down")
}

func handleStatus(stdout, stderr io.Writer) error {
	return runDockerComposeCmdInteractive(os.Stdin, stdout, stderr, "ps")
}

func handleLogs(follow bool, stdout, stderr io.Writer) error {
	if follow {
		return runDockerComposeCmdInteractive(os.Stdin, stdout, stderr, "logs", "-f")
	}
	return runDockerComposeCmdInteractive(os.Stdin, stdout, stderr, "logs")
}

func handleAuth(stdout, stderr io.Writer) error {
	return runDockerComposeCmdInteractive(os.Stdin, stdout, stderr, "exec", "appliance", "opencode", "auth", "login")
}

func handleOpen(stdout io.Writer) error {
	urlStr := loadSavedDashboardURL()
	fmt.Fprintf(stdout, "Opening dashboard: %s\n", urlStr)
	return openBrowser(urlStr)
}

func openBrowser(urlStr string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = execCommand("open", urlStr)
	case "linux", "freebsd", "netbsd", "openbsd":
		cmd = execCommand("xdg-open", urlStr)
	case "windows":
		cmd = execCommand("rundll32", "url.dll,FileProtocolHandler", urlStr)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	return cmd.Run()
}

func printUsage(out io.Writer) {
	fmt.Fprintln(out, `Usage: openprophet <command> [options]

Commands:
  install   Install and start the OpenProphet appliance
  start     Start the installed appliance
  stop      Stop the running appliance
  status    Show the status of the appliance
  logs      View logs from the appliance
  update    Update the appliance to the latest version
  auth      Login to OpenCode inside the appliance
  open      Open the appliance dashboard in your browser
  version   Show version information

Options for install and update:
  --key     The entitlement key (can also be set via OPENPROPHET_KEY env var)

Options for logs:
  --no-follow   Do not follow log output`)
}

func main() {
	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		os.Exit(1)
	}

	cmd := os.Args[1]

	if cmd == "version" || cmd == "-version" || cmd == "--version" {
		fmt.Printf("openprophet version %s\n", Version)
		return
	}

	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		printUsage(os.Stdout)
		return
	}

	var keyFlag string
	var followLogs bool = true

	switch cmd {
	case "install", "update":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		fs.StringVar(&keyFlag, "key", "", "entitlement key")
		fs.Parse(os.Args[2:])

		apiURL := os.Getenv("OPENPROPHET_API_URL")
		if apiURL == "" {
			apiURL = defaultAPIUrl
		}

		var err error
		if cmd == "install" {
			err = handleInstall(context.Background(), keyFlag, apiURL, os.Stdout, os.Stderr)
		} else {
			err = handleUpdate(context.Background(), keyFlag, apiURL, os.Stdout, os.Stderr)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "logs":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		var noFollow bool
		fs.BoolVar(&noFollow, "no-follow", false, "do not follow log output")
		fs.Parse(os.Args[2:])
		followLogs = !noFollow

		if err := handleLogs(followLogs, os.Stdout, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "start":
		if err := handleStart(os.Stdout, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "stop":
		if err := handleStop(os.Stdout, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "status":
		if err := handleStatus(os.Stdout, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "auth":
		if err := handleAuth(os.Stdout, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "open":
		if err := handleOpen(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage(os.Stderr)
		os.Exit(1)
	}
}
