package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetHomeDir(t *testing.T) {
	tempHome := t.TempDir()
	os.Setenv("OPENPROPHET_HOME", tempHome)
	defer os.Unsetenv("OPENPROPHET_HOME")

	home := getHomeDir()
	if home != tempHome {
		t.Errorf("expected home dir to be %q, got %q", tempHome, home)
	}
}

func TestValidateOrigin(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{"https://openprophet.io", false},
		{"https://dev.openprophet.io", false},
		{"http://localhost:8080", false},
		{"http://127.0.0.1:3737", false},
		{"http://[::1]:3737", false},
		{"http://openprophet.io", true},
		{"http://insecure-domain.com", true},
		{"insecure-domain.com", true},
	}

	for _, tt := range tests {
		err := validateOrigin(tt.url)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateOrigin(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
		}
	}
}

func TestValidateOriginInsecureLocal(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		url      string
		wantErr  bool
	}{
		// Case 1: Variable not set (or empty)
		{"missing_env_http_local", "", "http://appliance.local", true},
		{"missing_env_http_local_port", "", "http://appliance.local:8080", true},
		{"missing_env_https_local", "", "https://appliance.local", false},
		{"missing_env_http_loopback", "", "http://localhost:8080", false},

		// Case 2: Variable is set to a wrong value
		{"wrong_env_http_local_0", "0", "http://appliance.local", true},
		{"wrong_env_http_local_true", "true", "http://appliance.local", true},

		// Case 3: Variable is set to "1" (success cases)
		{"success_http_local", "1", "http://appliance.local", false},
		{"success_http_local_port", "1", "http://appliance.local:8080", false},
		{"success_http_local_trailing_dot", "1", "http://appliance.local.", false},
		{"success_http_local_case_insensitive", "1", "http://Appliance.LOCAL", false},
		{"success_http_sub_local", "1", "http://sub.appliance.local", false},
		{"success_https_local", "1", "https://appliance.local", false},

		// Case 4: Variable is set to "1" but URL should be rejected (hostname shape checks)
		{"reject_notreallylocal", "1", "http://notreallylocal", true},
		{"reject_evil_local_example", "1", "http://evil-local.example", true},
		{"reject_local", "1", "http://local", true},
		{"reject_dot_local", "1", "http://.local", true},
		{"reject_empty_label_local", "1", "http://foo..local", true},
		{"reject_notready_local_example", "1", "http://notready.local.example", true},
		{"reject_ftp_local", "1", "ftp://appliance.local", true},

		// Case 5: Variable is set to "1" but URL has prohibited parts
		{"reject_userinfo", "1", "http://user:pass@appliance.local", true},
		{"reject_query", "1", "http://appliance.local?query=1", true},
		{"reject_fragment", "1", "http://appliance.local#fragment", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				t.Setenv("OPENPROPHET_ALLOW_INSECURE_LOCAL", tt.envValue)
			} else {
				t.Setenv("OPENPROPHET_ALLOW_INSECURE_LOCAL", "")
			}
			err := validateOrigin(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateOrigin(%q) with env=%q: error = %v, wantErr %v", tt.url, tt.envValue, err, tt.wantErr)
			}
		})
	}
}

func TestValidateManifest(t *testing.T) {
	tests := []struct {
		m       Manifest
		wantErr bool
	}{
		{Manifest{SchemaVersion: 1, Image: "openprophet/runtime:v2.0.0"}, false},
		{Manifest{SchemaVersion: 1, Image: "openprophet/runtime@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, false},
		{Manifest{SchemaVersion: 0, Image: "openprophet/runtime:v2.0.0"}, true},
		{Manifest{SchemaVersion: 2, Image: "openprophet/runtime:v2.0.0"}, true},
		{Manifest{SchemaVersion: 1, Image: "openprophet/runtime:latest"}, true},
		{Manifest{SchemaVersion: 1, Image: "openprophet/runtime"}, true},
		{Manifest{SchemaVersion: 1, Image: "openprophet/runtime:v2\nBAD=value"}, true},
		{Manifest{SchemaVersion: 1, Image: "openprophet/runtime:v2", DashboardURL: "https://example.com"}, true},
		{Manifest{SchemaVersion: 2, Image: ""}, true},
	}

	for _, tt := range tests {
		err := validateManifest(&tt.m)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateManifest(%+v) error = %v, wantErr %v", tt.m, err, tt.wantErr)
		}
	}
}

func TestFetchManifest(t *testing.T) {
	key := "secret_key_123"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/appliance/manifest" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		authHeader := r.Header.Get("Authorization")
		if authHeader != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		m := Manifest{
			SchemaVersion: 1,
			Image:         "openprophet/runtime:v2.0.0",
			DashboardURL:  "http://127.0.0.1:3737",
		}
		json.NewEncoder(w).Encode(m)
	}))
	defer server.Close()

	// 1. Success case
	m, err := fetchManifest(context.Background(), server.URL, key)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if m.Image != "openprophet/runtime:v2.0.0" {
		t.Errorf("expected image to be openprophet/runtime:v2.0.0, got %q", m.Image)
	}

	// 2. Auth failure
	_, err = fetchManifest(context.Background(), server.URL, "wrong_key")
	if err == nil {
		t.Fatal("expected error on invalid auth, got nil")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("expected unauthorized error, got %v", err)
	}
	// Verify no key leakage in error message
	if strings.Contains(err.Error(), "wrong_key") {
		t.Error("error message leaked key!")
	}
}

func TestUpdateEnvFile(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")

	// 1. Write initial env file
	initialContent := `# Comments
OPENPROPHET_IMAGE=openprophet/runtime:v1.0.0
ANTHROPIC_API_KEY=user_anthropic_key
`
	if err := os.WriteFile(envPath, []byte(initialContent), 0644); err != nil {
		t.Fatalf("failed to write initial env: %v", err)
	}

	// 2. Update image
	newImage := "openprophet/runtime:v2.0.0"
	if err := updateEnvFile(envPath, newImage); err != nil {
		t.Fatalf("failed to update env file: %v", err)
	}

	// 3. Verify content
	contentBytes, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("failed to read updated env: %v", err)
	}
	content := string(contentBytes)

	if !strings.Contains(content, "OPENPROPHET_IMAGE="+newImage) {
		t.Errorf("expected new image %q to be in env, got:\n%s", newImage, content)
	}
	if !strings.Contains(content, "ANTHROPIC_API_KEY=user_anthropic_key") {
		t.Errorf("user-managed credentials were not preserved, got:\n%s", content)
	}

	// Verify permissions are 0600
	info, err := os.Stat(envPath)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("expected mode 0600, got %o", perm)
	}
}

func TestGeneratedComposeIsLocalPersistentAndSecretFree(t *testing.T) {
	secret := "op_secret_that_must_not_appear"
	if strings.Contains(composeTemplate, secret) {
		t.Fatal("compose template leaked an entitlement key")
	}
	for _, expected := range []string{
		`127.0.0.1:3737:3737`,
		`openprophet_data:/app/data`,
		`no-new-privileges:true`,
		`http://127.0.0.1:3737/api/health`,
	} {
		if !strings.Contains(composeTemplate, expected) {
			t.Errorf("compose template missing %q", expected)
		}
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 {
		if args[0] == "--" {
			args = args[1:]
			break
		}
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "No command\n")
		os.Exit(2)
	}

	cmd := args[0]
	subArgs := args[1:]

	switch cmd {
	case "docker":
		if len(subArgs) >= 2 && subArgs[0] == "compose" && subArgs[1] == "version" {
			fmt.Println("docker-compose version 2.20.0")
			os.Exit(0)
		}
		// Print the run trace for testing assertions
		fmt.Printf("MOCK_DOCKER_RUN: %s\n", strings.Join(subArgs, " "))
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command %q\n", cmd)
		os.Exit(1)
	}
}

func mockExecCommand(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
	return cmd
}

func TestInstall(t *testing.T) {
	// Set mock exec Command
	oldExecCommand := execCommand
	execCommand = mockExecCommand
	defer func() { execCommand = oldExecCommand }()

	tempHome := t.TempDir()
	os.Setenv("OPENPROPHET_HOME", tempHome)
	defer os.Unsetenv("OPENPROPHET_HOME")

	// Set up httptest server for manifest
	key := "test-token"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		m := Manifest{
			SchemaVersion: 1,
			Image:         "openprophet/runtime:v2.0.0",
			DashboardURL:  "http://127.0.0.1:3737",
		}
		json.NewEncoder(w).Encode(m)
	}))
	defer ts.Close()

	var stdoutBuf, stderrBuf strings.Builder
	err := handleInstall(context.Background(), key, ts.URL, &stdoutBuf, &stderrBuf)
	if err != nil {
		t.Fatalf("handleInstall failed: %v", err)
	}

	output := stdoutBuf.String()
	if !strings.Contains(output, "Pulling appliance images...") {
		t.Errorf("unexpected output: %s", output)
	}
	if strings.Contains(output+stderrBuf.String(), key) {
		t.Fatal("install output leaked entitlement key")
	}
	if !strings.Contains(output, "MOCK_DOCKER_RUN: compose pull") || !strings.Contains(output, "MOCK_DOCKER_RUN: compose up -d") {
		t.Fatalf("unexpected Docker lifecycle commands: %s", output)
	}

	// Verify key file exists and has 0600 permissions
	keyPath := filepath.Join(tempHome, "entitlement.key")
	data, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("failed to read key file: %v", err)
	}
	if string(data) != key {
		t.Errorf("expected saved key to be %q, got %q", key, string(data))
	}
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected 0600 permissions on key file, got %o", info.Mode().Perm())
	}

	// Verify Compose file was generated
	composePath := filepath.Join(tempHome, "docker-compose.yml")
	composeBytes, err := os.ReadFile(composePath)
	if err != nil {
		t.Errorf("docker-compose.yml was not generated: %v", err)
	}
	if strings.Contains(string(composeBytes), key) {
		t.Fatal("generated compose file leaked entitlement key")
	}
}

func TestUpdatePreservesCredentialsAndUsesSavedEntitlement(t *testing.T) {
	oldExecCommand := execCommand
	execCommand = mockExecCommand
	defer func() { execCommand = oldExecCommand }()

	t.Setenv("OPENPROPHET_HOME", t.TempDir())
	key := "op_saved_secret"
	if err := saveEntitlementKey(key); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(getHomeDir(), ".env")
	if err := os.WriteFile(envPath, []byte("OPENPROPHET_IMAGE=example/old:v1\nANTHROPIC_API_KEY=local-only\n"), 0644); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+key {
			t.Errorf("unexpected authorization header")
		}
		json.NewEncoder(w).Encode(Manifest{SchemaVersion: 1, Image: "example/openprophet:v2", DashboardURL: "http://127.0.0.1:3737"})
	}))
	defer server.Close()

	var stdout, stderr strings.Builder
	if err := handleUpdate(context.Background(), "", server.URL, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "OPENPROPHET_IMAGE=example/openprophet:v2") || !strings.Contains(string(content), "ANTHROPIC_API_KEY=local-only") {
		t.Fatalf("update did not preserve local environment: %s", content)
	}
	if mode, err := os.Stat(envPath); err != nil || mode.Mode().Perm() != 0600 {
		t.Fatalf("updated environment permissions are not 0600")
	}
	if strings.Contains(stdout.String()+stderr.String(), key) {
		t.Fatal("update output leaked entitlement key")
	}
}
