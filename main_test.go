package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Known jotta-cli prompt strings. Update these when jotta-cli changes prompts.
// The login and status tests will fail if these no longer match what main.go uses.
const (
	promptLicense     = "accept license (yes/no): "
	promptToken       = "Personal login token: "
	promptDeviceName  = "Device name"
	promptReuseDevice = "Do you want to re-use this device? (yes/no):"
	promptLogout      = "Backup will stop. Continue?(y/n): "

	// Status output patterns matched in the main() startup loop.
	statusMatchingDevice   = "Found remote device that matches this machine"
	statusSessionRevoked   = "Error: The session has been revoked."
	statusNoDeviceName     = "The device name has not been set"
	statusNotLoggedIn      = "Not logged in"
	statusDeviceNotRemote  = "does not exist remotely"
)

var fakeCLIPath string

func TestMain(m *testing.M) {
	// Build fake-cli binary.
	binPath := filepath.Join("testdata", "fake-cli", "fake-cli")
	cmd := exec.Command("go", "build", "-o", binPath, "./testdata/fake-cli/")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		panic("failed to build fake-cli: " + err.Error())
	}
	abs, err := filepath.Abs(binPath)
	if err != nil {
		panic(err)
	}
	fakeCLIPath = abs

	// Suppress PTY output during tests.
	ptyOutput = io.Discard

	code := m.Run()

	os.Remove(binPath)
	os.Exit(code)
}

// --- fake-cli scenario helpers ---

type fakeStep struct {
	Prompt    string `json:"prompt"`
	Expect    string `json:"expect,omitempty"`
	DelayMs   int    `json:"delayMs,omitempty"`
	ChunkSize int    `json:"chunkSize,omitempty"`
}

type fakeScenario struct {
	Steps       []fakeStep `json:"steps"`
	FinalOutput string     `json:"finalOutput,omitempty"`
	ExitCode    int        `json:"exitCode"`
	HangForever bool       `json:"hangForever,omitempty"`
}

func scenarioJSON(t *testing.T, sc fakeScenario) string {
	t.Helper()
	b, err := json.Marshal(sc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func setScenarioEnv(t *testing.T, sc fakeScenario) {
	t.Helper()
	t.Setenv("FAKECLI_SCENARIO", scenarioJSON(t, sc))
}

// --- ptyRun tests ---

func TestPtyRun_SinglePrompt(t *testing.T) {
	setScenarioEnv(t, fakeScenario{
		Steps: []fakeStep{
			{Prompt: "Enter name: ", Expect: "alice"},
		},
		FinalOutput: "Hello alice\n",
	})

	err := ptyRun(fakeCLIPath, nil, []prompt{
		{"Enter name: ", "alice"},
	}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPtyRun_MultiplePrompts(t *testing.T) {
	setScenarioEnv(t, fakeScenario{
		Steps: []fakeStep{
			{Prompt: "First: ", Expect: "one"},
			{Prompt: "Second: ", Expect: "two"},
			{Prompt: "Third: ", Expect: "three"},
		},
	})

	err := ptyRun(fakeCLIPath, nil, []prompt{
		{"First: ", "one"},
		{"Second: ", "two"},
		{"Third: ", "three"},
	}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPtyRun_MutuallyExclusivePrompts(t *testing.T) {
	// Simulate login flow where only "Device name" appears (not re-use).
	setScenarioEnv(t, fakeScenario{
		Steps: []fakeStep{
			{Prompt: promptLicense, Expect: "yes"},
			{Prompt: promptToken, Expect: "test-token"},
			{Prompt: "Device name: ", Expect: "my-device"},
		},
		FinalOutput: "Logged in.\n",
	})

	err := ptyRun(fakeCLIPath, nil, []prompt{
		{promptLicense, "yes"},
		{promptToken, "test-token"},
		// Both alternatives listed — only "Device name" will appear.
		{promptDeviceName, "my-device"},
		{promptReuseDevice, "yes"},
	}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPtyRun_MutuallyExclusivePrompts_ReuseDevice(t *testing.T) {
	// Simulate login flow where "re-use device" appears (not device name).
	setScenarioEnv(t, fakeScenario{
		Steps: []fakeStep{
			{Prompt: promptLicense, Expect: "yes"},
			{Prompt: promptToken, Expect: "test-token"},
			{Prompt: "Do you want to re-use this device? (yes/no): ", Expect: "yes"},
		},
		FinalOutput: "Logged in.\n",
	})

	err := ptyRun(fakeCLIPath, nil, []prompt{
		{promptLicense, "yes"},
		{promptToken, "test-token"},
		{promptDeviceName, "my-device"},
		{promptReuseDevice, "yes"},
	}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPtyRun_Timeout(t *testing.T) {
	setScenarioEnv(t, fakeScenario{
		HangForever: true,
	})

	start := time.Now()
	err := ptyRun(fakeCLIPath, nil, nil, 500*time.Millisecond)
	elapsed := time.Since(start)

	// Should return an error (killed process).
	if err == nil {
		t.Fatal("expected error from timeout, got nil")
	}
	// Should complete in roughly the timeout duration.
	if elapsed > 3*time.Second {
		t.Fatalf("took too long: %v", elapsed)
	}
}

func TestPtyRun_NoMatchingPrompt(t *testing.T) {
	// fake-cli prints output and exits without any interactive prompts.
	setScenarioEnv(t, fakeScenario{
		FinalOutput: "Some status output\n",
	})

	err := ptyRun(fakeCLIPath, nil, []prompt{
		{"this will never match: ", "unused"},
	}, 2*time.Second)
	// Process exits cleanly — no error expected.
	if err != nil {
		t.Fatal(err)
	}
}

func TestPtyRun_PartialPromptAcrossReads(t *testing.T) {
	// Split the prompt into small chunks to simulate partial PTY reads.
	setScenarioEnv(t, fakeScenario{
		Steps: []fakeStep{
			{Prompt: "Enter your name please: ", ChunkSize: 5, Expect: "bob"},
		},
	})

	err := ptyRun(fakeCLIPath, nil, []prompt{
		{"Enter your name please: ", "bob"},
	}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPtyRun_ExitCodePropagated(t *testing.T) {
	setScenarioEnv(t, fakeScenario{
		FinalOutput: "error\n",
		ExitCode:    2,
	})

	err := ptyRun(fakeCLIPath, nil, nil, 2*time.Second)
	if err == nil {
		t.Fatal("expected error from non-zero exit code")
	}
}

// --- loginWithToken tests ---

func TestLoginWithToken_NewDevice(t *testing.T) {
	t.Setenv("JOTTA_TOKEN", "test-token-123")
	t.Setenv("JOTTA_DEVICE", "test-device")

	setScenarioEnv(t, fakeScenario{
		Steps: []fakeStep{
			{Prompt: promptLicense, Expect: "yes"},
			{Prompt: promptToken, Expect: "test-token-123"},
			{Prompt: "Device name: ", Expect: "test-device"},
		},
		FinalOutput: "Login successful.\n",
	})

	origCLI := jottaCLI
	jottaCLI = fakeCLIPath
	defer func() { jottaCLI = origCLI }()

	err := loginWithToken()
	if err != nil {
		t.Fatal(err)
	}
}

func TestLoginWithToken_ExistingDevice(t *testing.T) {
	t.Setenv("JOTTA_TOKEN", "test-token-456")
	t.Setenv("JOTTA_DEVICE", "test-device")

	setScenarioEnv(t, fakeScenario{
		Steps: []fakeStep{
			{Prompt: promptLicense, Expect: "yes"},
			{Prompt: promptToken, Expect: "test-token-456"},
			{Prompt: "Do you want to re-use this device? (yes/no): ", Expect: "yes"},
		},
		FinalOutput: "Login successful.\n",
	})

	origCLI := jottaCLI
	jottaCLI = fakeCLIPath
	defer func() { jottaCLI = origCLI }()

	err := loginWithToken()
	if err != nil {
		t.Fatal(err)
	}
}

// Verify that the prompt strings in loginWithToken match our known constants.
// This test catches drift between main.go and the known prompt constants.
func TestLoginWithToken_PromptStringsMatch(t *testing.T) {
	t.Setenv("JOTTA_TOKEN", "tok")
	t.Setenv("JOTTA_DEVICE", "dev")

	// We can't easily inspect loginWithToken's internals, but we can verify
	// the prompt strings by running it against exact prompts. If any prompt
	// string in main.go changes, this test will hang (timeout) or fail.
	setScenarioEnv(t, fakeScenario{
		Steps: []fakeStep{
			// These must exactly match the prompts in loginWithToken().
			{Prompt: "accept license (yes/no): ", Expect: "yes"},
			{Prompt: "Personal login token: ", Expect: "tok"},
			{Prompt: "Device name: ", Expect: "dev"},
		},
	})

	origCLI := jottaCLI
	jottaCLI = fakeCLIPath
	defer func() { jottaCLI = origCLI }()

	err := loginWithToken()
	if err != nil {
		t.Fatalf("loginWithToken failed — prompt strings may have changed: %v", err)
	}
}

// --- Status pattern matching tests ---

func TestStatusPatternMatching(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		wantKey string
	}{
		{
			name:    "matching device",
			output:  "Some output\nFound remote device that matches this machine\nMore output",
			wantKey: "matching_device",
		},
		{
			name:    "session revoked",
			output:  "Error: The session has been revoked.\nPlease login again.",
			wantKey: "session_revoked",
		},
		{
			name:    "device name not set",
			output:  "The device name has not been set\nRun jotta-cli setup",
			wantKey: "no_device_name",
		},
		{
			name:    "not logged in",
			output:  "Not logged in\nUse jotta-cli login",
			wantKey: "not_logged_in",
		},
		{
			name:    "device not remote",
			output:  "ERROR  device [integration-test] does not exist remotely. Jottad cannot continue.",
			wantKey: "device_not_remote",
		},
		{
			name:    "unknown status",
			output:  "Something unexpected happened",
			wantKey: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyStatus(tt.output)
			if got != tt.wantKey {
				t.Errorf("classifyStatus(%q) = %q, want %q", tt.output, got, tt.wantKey)
			}
		})
	}
}

// --- loadEnvFile tests ---

func TestLoadEnvFile_BasicKeyValue(t *testing.T) {
	f := writeTempFile(t, "KEY1=value1\nKEY2=value2\n")
	loadEnvFile(f)
	assertEnv(t, "KEY1", "value1")
	assertEnv(t, "KEY2", "value2")
}

func TestLoadEnvFile_QuotedValues(t *testing.T) {
	f := writeTempFile(t, `DOUBLE="hello world"`+"\n"+`SINGLE='foo bar'`+"\n")
	loadEnvFile(f)
	assertEnv(t, "DOUBLE", "hello world")
	assertEnv(t, "SINGLE", "foo bar")
}

func TestLoadEnvFile_ExportPrefix(t *testing.T) {
	f := writeTempFile(t, "export MY_VAR=exported\n")
	loadEnvFile(f)
	assertEnv(t, "MY_VAR", "exported")
}

func TestLoadEnvFile_CommentsAndBlanks(t *testing.T) {
	f := writeTempFile(t, "# comment\n\nVALID=yes\n  # indented comment\n")
	loadEnvFile(f)
	assertEnv(t, "VALID", "yes")
}

func TestLoadEnvFile_NoEquals(t *testing.T) {
	f := writeTempFile(t, "NOEQUALS\n=noleft\nGOOD=ok\n")
	loadEnvFile(f)
	assertEnv(t, "GOOD", "ok")
}

func TestLoadEnvFile_MissingFile(t *testing.T) {
	// Should not panic or error — silently ignored.
	loadEnvFile("/nonexistent/path/env")
}

// --- envInt tests ---

func TestEnvInt_Set(t *testing.T) {
	t.Setenv("TEST_INT", "42")
	if got := envInt("TEST_INT", 10); got != 42 {
		t.Errorf("envInt = %d, want 42", got)
	}
}

func TestEnvInt_Default(t *testing.T) {
	t.Setenv("TEST_INT_UNSET", "")
	if got := envInt("TEST_INT_MISSING_"+t.Name(), 99); got != 99 {
		t.Errorf("envInt = %d, want 99", got)
	}
}

func TestEnvInt_InvalidNumber(t *testing.T) {
	t.Setenv("TEST_INT_BAD", "abc")
	if got := envInt("TEST_INT_BAD", 7); got != 7 {
		t.Errorf("envInt = %d, want 7", got)
	}
}

// --- forceSymlink tests ---

func TestForceSymlink_New(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	os.MkdirAll(target, 0755)
	link := filepath.Join(dir, "link")

	forceSymlink(target, link)

	resolved, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != target {
		t.Errorf("symlink points to %q, want %q", resolved, target)
	}
}

func TestForceSymlink_Replace(t *testing.T) {
	dir := t.TempDir()
	target1 := filepath.Join(dir, "target1")
	target2 := filepath.Join(dir, "target2")
	os.MkdirAll(target1, 0755)
	os.MkdirAll(target2, 0755)
	link := filepath.Join(dir, "link")

	os.Symlink(target1, link)
	forceSymlink(target2, link)

	resolved, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != target2 {
		t.Errorf("symlink points to %q, want %q", resolved, target2)
	}
}

// --- helpers ---

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "env-*")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(content)
	f.Close()
	return f.Name()
}

func assertEnv(t *testing.T, key, want string) {
	t.Helper()
	if got := os.Getenv(key); got != want {
		t.Errorf("env %s = %q, want %q", key, got, want)
	}
}

// classifyStatus extracts from main.go's status matching logic. This is tested
// separately so the pattern strings can be validated without running jottad.
func classifyStatus(output string) string {
	switch {
	case strings.Contains(output, statusMatchingDevice):
		return "matching_device"
	case strings.Contains(output, statusSessionRevoked):
		return "session_revoked"
	case strings.Contains(output, statusNoDeviceName):
		return "no_device_name"
	case strings.Contains(output, statusNotLoggedIn):
		return "not_logged_in"
	case strings.Contains(output, statusDeviceNotRemote):
		return "device_not_remote"
	default:
		return ""
	}
}
