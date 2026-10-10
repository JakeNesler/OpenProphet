package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

const maxApplianceArchiveBytes int64 = 2 << 30

// Manifest defines the contract for fetching the appliance manifest.
type Manifest struct {
	SchemaVersion int      `json:"schemaVersion"`
	Image         string   `json:"image"`
	Architecture  string   `json:"architecture"`
	DashboardURL  string   `json:"dashboardUrl,omitempty"`
	Delivery      Delivery `json:"delivery"`
}

type Delivery struct {
	Kind      string `json:"kind"`
	Format    string `json:"format"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expiresAt"`
	SHA256    string `json:"sha256"`
}

// Allow mocking of commands in tests.
var execCommand = exec.Command

// Allow overriding the detected host architecture in tests.
var runtimeArch = runtime.GOARCH

// hostArchitecture maps the host architecture onto the appliance architectures
// the manifest API can deliver.
func hostArchitecture() (string, error) {
	switch runtimeArch {
	case "amd64", "arm64":
		return runtimeArch, nil
	}
	return "", fmt.Errorf("unsupported host architecture: %s", runtimeArch)
}

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

func validateManifest(m *Manifest, arch string) error {
	if m.SchemaVersion != 2 {
		return fmt.Errorf("unsupported manifest schema version")
	}
	if m.Architecture != arch {
		return fmt.Errorf("malformed manifest: architecture does not match this host")
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
	if m.Delivery.Kind != "docker-image-archive" {
		return fmt.Errorf("malformed manifest: unsupported delivery kind")
	}
	if m.Delivery.Format != "docker-tar-gzip" {
		return fmt.Errorf("malformed manifest: unsupported delivery format")
	}
	if err := validateDeliveryURL(m.Delivery.URL); err != nil {
		return err
	}
	expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(m.Delivery.ExpiresAt))
	if err != nil {
		return fmt.Errorf("malformed manifest: invalid delivery expiry")
	}
	if !time.Now().Before(expiresAt) {
		return fmt.Errorf("malformed manifest: delivery URL has expired")
	}
	checksum, err := hex.DecodeString(strings.TrimSpace(m.Delivery.SHA256))
	if err != nil || len(checksum) != sha256.Size {
		return fmt.Errorf("malformed manifest: invalid archive checksum")
	}
	return nil
}

func validateDeliveryURL(rawURL string) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("malformed manifest: invalid delivery URL")
	}
	if parsed.Scheme == "https" {
		return nil
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if parsed.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1") {
		return nil
	}
	return fmt.Errorf("malformed manifest: delivery URL must use HTTPS")
}

// manifestEndpoint builds the manifest URL for the requested architecture. The
// entitlement key is never part of the URL; it travels in the Authorization header.
func manifestEndpoint(apiURL string, arch string) (string, error) {
	parsed, err := url.Parse(strings.TrimSuffix(apiURL, "/"))
	if err != nil {
		return "", fmt.Errorf("invalid API URL")
	}
	endpoint := parsed.JoinPath("api", "appliance", "manifest")
	query := url.Values{}
	query.Set("arch", arch)
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func fetchManifest(ctx context.Context, apiURL string, key string) (*Manifest, error) {
	if err := validateOrigin(apiURL); err != nil {
		return nil, err
	}

	arch, err := hostArchitecture()
	if err != nil {
		return nil, err
	}

	endpoint, err := manifestEndpoint(apiURL, arch)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
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

	if err := validateManifest(&m, arch); err != nil {
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
	saved := struct {
		SchemaVersion int    `json:"schemaVersion"`
		Image         string `json:"image"`
		Architecture  string `json:"architecture"`
		DashboardURL  string `json:"dashboardUrl,omitempty"`
	}{
		SchemaVersion: m.SchemaVersion,
		Image:         m.Image,
		Architecture:  m.Architecture,
		DashboardURL:  m.DashboardURL,
	}
	data, err := json.MarshalIndent(saved, "", "  ")
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
    pull_policy: never
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

// checkDockerDaemon proves the CLI can actually reach the daemon. `docker compose version`
// never opens the socket, so a user who is not in the `docker` group sailed through the
// preflight, downloaded the entitlement-gated archive, and only then hit an opaque
// "failed to load appliance image". Run this before any download.
func checkDockerDaemon() error {
	// `docker version` exits non-zero when the daemon is unreachable or the socket is denied;
	// `docker info` prints the error but still exits 0, which would let this check pass.
	cmd := execCommand("docker", "version", "--format", "{{.Server.Version}}")
	var stderr strings.Builder
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		return nil
	}
	detail := strings.TrimSpace(stderr.String())
	if i := strings.IndexByte(detail, '\n'); i >= 0 {
		detail = detail[:i]
	}
	if strings.Contains(detail, "permission denied") && strings.Contains(detail, "docker.sock") {
		return fmt.Errorf("docker is installed but this user cannot access the Docker daemon (%s).\n"+
			"Add yourself to the docker group, start a new login shell, and re-run the install:\n"+
			"  sudo usermod -aG docker \"$USER\"\n"+
			"  newgrp docker        # or log out and back in\n"+
			"  openprophet install\n"+
			"Do not run the launcher with sudo. On rootless Docker or Docker Desktop, point DOCKER_HOST at your user socket instead", detail)
	}
	if detail == "" {
		detail = "docker info failed"
	}
	return fmt.Errorf("the Docker daemon is not reachable (%s). Start Docker, confirm `docker info` works, then re-run", detail)
}

func checkDockerCLI() error {
	cmd := execCommand("docker", "--version")
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker is not installed or not on PATH. Install Docker Engine (Linux: https://docs.docker.com/engine/install/) or Docker Desktop (macOS: https://docs.docker.com/desktop/), then re-run")
	}
	return nil
}

// runPreflight is the shared gate for install and update: CLI, compose plugin, then daemon
// access, each failing with an actionable message before anything is downloaded.
func runPreflight() error {
	for _, check := range []func() error{checkDockerCLI, checkDockerCompose, checkDockerDaemon} {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

// dockerOutput runs a docker subcommand and returns its trimmed stdout, or the first line of
// stderr as the error.
func dockerOutput(args ...string) (string, error) {
	cmd := execCommand("docker", args...)
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errOut.String())
		if i := strings.IndexByte(msg, '\n'); i >= 0 {
			msg = msg[:i]
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// handleDoctor prints one line per prerequisite so a user (or a support thread) can see at a
// glance what is missing before — or after — an install. Informational rows use "-" and never
// fail the run; only the Docker rows can.
func handleDoctor(stdout io.Writer) error {
	fmt.Fprintf(stdout, "openprophet %s — doctor\n", Version)
	failed := 0
	row := func(mark, name, detail string) {
		fmt.Fprintf(stdout, "  %s %-9s %s\n", mark, name, detail)
	}
	fail := func(name, detail string) { failed++; row("✗", name, detail) }

	row("✓", "host", runtime.GOOS+"/"+runtime.GOARCH)

	if out, err := dockerOutput("--version"); err != nil {
		fail("docker", "not installed or not on PATH — https://docs.docker.com/get-docker/")
	} else {
		row("✓", "docker", out)
	}

	if out, err := dockerOutput("compose", "version"); err != nil {
		fail("compose", "docker compose plugin not available — "+err.Error())
	} else {
		row("✓", "compose", out)
	}

	if err := checkDockerDaemon(); err != nil {
		lines := strings.Split(err.Error(), "\n")
		fail("daemon", lines[0])
		for _, l := range lines[1:] {
			fmt.Fprintf(stdout, "              %s\n", l)
		}
	} else {
		server, _ := dockerOutput("version", "--format", "{{.Server.Version}}")
		row("✓", "daemon", "reachable (server "+server+")")
	}

	home := getHomeDir()
	if _, err := loadEntitlementKey(); err == nil {
		row("-", "key", "saved in "+home)
	} else {
		row("-", "key", "not saved yet — `openprophet install` will ask for it")
	}
	if _, err := os.Stat(filepath.Join(home, "manifest.json")); err == nil {
		row("-", "appliance", "installed — `openprophet status` for the container")
	} else {
		row("-", "appliance", "not installed yet")
	}

	if failed > 0 {
		return fmt.Errorf("%d check(s) failed — fix the ✗ items above and run `openprophet doctor` again", failed)
	}
	fmt.Fprintln(stdout, "All checks passed.")
	return nil
}

// Prompting for the key only makes sense on a real terminal; a piped stdin (the installer
// script, CI) keeps the explicit "key is required" error. Both are variables so tests can
// simulate a terminal.
var (
	stdinReader     io.Reader = os.Stdin
	stdinIsTerminal           = func() bool {
		fi, err := os.Stdin.Stat()
		return err == nil && fi.Mode()&os.ModeCharDevice != 0
	}
)

// resolveEntitlementKey: --key flag, then OPENPROPHET_KEY, then the key saved by a previous
// install, then an interactive prompt. `missing` is returned when none of those yield a key.
func resolveEntitlementKey(keyFlag string, stdout io.Writer, missing error) (string, error) {
	if keyFlag != "" {
		return keyFlag, nil
	}
	if v := os.Getenv("OPENPROPHET_KEY"); v != "" {
		return v, nil
	}
	if saved, err := loadEntitlementKey(); err == nil && saved != "" {
		return saved, nil
	}
	if !stdinIsTerminal() {
		return "", missing
	}
	fmt.Fprint(stdout, "Entitlement key (op_..., from your openprophet.io guides page): ")
	line, err := bufio.NewReader(stdinReader).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	key := strings.TrimSpace(line)
	if key == "" {
		return "", missing
	}
	if !strings.HasPrefix(key, "op_") {
		return "", fmt.Errorf("entitlement keys start with op_ — copy it from the guides page")
	}
	return key, nil
}

func printNextSteps(stdout io.Writer) {
	fmt.Fprintln(stdout, `
Next steps:
  openprophet open      open the dashboard in your browser
  openprophet auth      connect your model provider inside the appliance
  openprophet status    container status        openprophet logs    follow output
  openprophet update    pull the latest approved image (your data is preserved)
  openprophet doctor    re-check Docker and the install at any time`)
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

func downloadApplianceArchive(ctx context.Context, m *Manifest) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.Delivery.URL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create appliance download request")
	}

	client := &http.Client{
		Timeout: 2 * time.Hour,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return validateDeliveryURL(req.URL.String())
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download appliance archive")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download appliance archive: server returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxApplianceArchiveBytes {
		return "", fmt.Errorf("appliance archive exceeds size limit")
	}

	archive, err := os.CreateTemp("", "openprophet-appliance-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("failed to create temporary appliance archive")
	}
	archivePath := archive.Name()
	removeArchive := true
	defer func() {
		archive.Close()
		if removeArchive {
			os.Remove(archivePath)
		}
	}()
	if err := archive.Chmod(0600); err != nil {
		return "", fmt.Errorf("failed to secure temporary appliance archive")
	}

	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(archive, hasher), io.LimitReader(resp.Body, maxApplianceArchiveBytes+1))
	if err != nil {
		return "", fmt.Errorf("failed to download appliance archive")
	}
	if written > maxApplianceArchiveBytes {
		return "", fmt.Errorf("appliance archive exceeds size limit")
	}
	if err := archive.Close(); err != nil {
		return "", fmt.Errorf("failed to finalize appliance archive")
	}
	actualChecksum := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actualChecksum, strings.TrimSpace(m.Delivery.SHA256)) {
		return "", fmt.Errorf("appliance archive checksum mismatch")
	}

	removeArchive = false
	return archivePath, nil
}

func loadAndVerifyAppliance(ctx context.Context, m *Manifest, stdout, stderr io.Writer) error {
	archivePath, err := downloadApplianceArchive(ctx, m)
	if err != nil {
		return err
	}
	defer os.Remove(archivePath)

	load := execCommand("docker", "image", "load", "--input", archivePath)
	load.Stdout = stdout
	load.Stderr = stderr
	if err := load.Run(); err != nil {
		return fmt.Errorf("failed to load appliance image")
	}

	inspect := execCommand("docker", "image", "inspect", "--format", "{{.Id}}", m.Image)
	inspect.Stdout = io.Discard
	inspect.Stderr = io.Discard
	if err := inspect.Run(); err != nil {
		return fmt.Errorf("loaded archive does not contain the manifest image")
	}
	return nil
}

func handleInstall(ctx context.Context, keyFlag string, apiURL string, stdout, stderr io.Writer) error {
	if err := runPreflight(); err != nil {
		return err
	}

	key, err := resolveEntitlementKey(keyFlag, stdout,
		fmt.Errorf("entitlement key is required (specify via --key or OPENPROPHET_KEY env var)"))
	if err != nil {
		return err
	}

	m, err := fetchManifest(ctx, apiURL, key)
	if err != nil {
		return err
	}

	fmt.Fprintln(stdout, "Downloading and verifying appliance image...")
	if err := loadAndVerifyAppliance(ctx, m, stdout, stderr); err != nil {
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

	fmt.Fprintln(stdout, "Starting appliance container...")
	if err := runDockerComposeCmdInteractive(os.Stdin, stdout, stderr, "up", "-d", "--pull", "never"); err != nil {
		return fmt.Errorf("failed to start container: %v", err)
	}

	fmt.Fprintln(stdout, "OpenProphet appliance installed and started successfully.")
	printNextSteps(stdout)
	return nil
}

func handleUpdate(ctx context.Context, keyFlag string, apiURL string, stdout, stderr io.Writer) error {
	if err := runPreflight(); err != nil {
		return err
	}

	key, err := resolveEntitlementKey(keyFlag, stdout,
		fmt.Errorf("entitlement key not found (run install first, or specify via --key/OPENPROPHET_KEY)"))
	if err != nil {
		return err
	}

	m, err := fetchManifest(ctx, apiURL, key)
	if err != nil {
		return err
	}

	fmt.Fprintln(stdout, "Downloading and verifying updated appliance image...")
	if err := loadAndVerifyAppliance(ctx, m, stdout, stderr); err != nil {
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

	fmt.Fprintln(stdout, "Recreating appliance container...")
	if err := runDockerComposeCmdInteractive(os.Stdin, stdout, stderr, "up", "-d", "--pull", "never"); err != nil {
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
  doctor    Check Docker, the daemon, and the install state (run this first if anything fails)
  install   Install and start the OpenProphet appliance
  start     Start the installed appliance
  stop      Stop the running appliance
  status    Show the status of the appliance
  logs      View logs from the appliance
  update    Update the appliance to the latest version
  auth      Login to OpenCode inside the appliance
  open      Open the appliance dashboard in your browser
  version   Show version information
  help      Show this help (also: openprophet <command> --help)

Options for install and update:
  --key     The entitlement key. Also read from OPENPROPHET_KEY, then from the key saved by a
            previous install; on a terminal you are prompted if none is set.

Options for logs:
  --no-follow   Do not follow log output

Environment:
  OPENPROPHET_KEY       Entitlement key (alternative to --key)
  OPENPROPHET_HOME      Where the appliance state lives (default: ~/.openprophet)`)
}

func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "help" {
			return true
		}
	}
	return false
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

	// `openprophet <command> --help` for commands that take no flags.
	switch cmd {
	case "doctor", "start", "stop", "status", "auth", "open":
		if wantsHelp(os.Args[2:]) {
			printUsage(os.Stdout)
			return
		}
	}

	switch cmd {
	case "doctor":
		if err := handleDoctor(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "install", "update":
		fs := flag.NewFlagSet(cmd, flag.ExitOnError)
		fs.StringVar(&keyFlag, "key", "", "entitlement key (or OPENPROPHET_KEY; prompted on a terminal)")
		fs.Usage = func() {
			fmt.Fprintf(os.Stderr, "Usage: openprophet %s [--key op_...]\n\n", cmd)
			fs.PrintDefaults()
		}
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
