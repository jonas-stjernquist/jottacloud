package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var fakeCLIPath string

func TestMain(m *testing.M) {
	// Build fake-cli binary.
	binPath := filepath.Join("testdata", "fake-cli", "fake-cli")
	cmd := exec.Command("go", "build", "-o", "fake-cli", ".")
	cmd.Dir = filepath.Join("testdata", "fake-cli")
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

	if err := os.Remove(binPath); err != nil {
		fmt.Fprintf(os.Stderr, "failed to remove fake-cli binary: %v\n", err)
	}
	os.Exit(code)
}

// --- fake-cli scenario helpers ---

type fakeStep struct {
	Prompt              string   `json:"prompt"`
	PromptSuffix        string   `json:"promptSuffix,omitempty"`
	PromptSuffixDelayMs int      `json:"promptSuffixDelayMs,omitempty"`
	Expect              string   `json:"expect,omitempty"`
	ExpectQueryReplies  []string `json:"expectQueryReplies,omitempty"`
	QuietMs             int      `json:"quietMs,omitempty"`
	DelayMs             int      `json:"delayMs,omitempty"`
	ChunkSize           int      `json:"chunkSize,omitempty"`
}

type fakeScenario struct {
	Steps       []fakeStep `json:"steps"`
	FinalOutput string     `json:"finalOutput,omitempty"`
	ExitCode    int        `json:"exitCode"`
	HangForever bool       `json:"hangForever,omitempty"`
	RawMode     bool       `json:"rawMode,omitempty"`
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

	err := ptyRun(context.Background(), fakeCLIPath, nil, []prompt{
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

	err := ptyRun(context.Background(), fakeCLIPath, nil, []prompt{
		{"First: ", "one"},
		{"Second: ", "two"},
		{"Third: ", "three"},
	}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPtyRun_RedactsTokenEcho(t *testing.T) {
	setScenarioEnv(t, fakeScenario{
		Steps: []fakeStep{
			{Prompt: promptToken, Expect: "secret-token"},
		},
		FinalOutput: "Logged in.\n",
	})

	var output bytes.Buffer
	oldOutput := ptyOutput
	ptyOutput = &output
	defer func() { ptyOutput = oldOutput }()

	err := ptyRun(context.Background(), fakeCLIPath, nil, []prompt{
		{promptToken, "secret-token"},
	}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "secret-token") {
		t.Fatalf("token leaked in PTY output: %q", output.String())
	}
	if !strings.Contains(output.String(), "[redacted]") {
		t.Fatalf("expected redacted token marker in PTY output: %q", output.String())
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

	err := ptyRun(context.Background(), fakeCLIPath, nil, []prompt{
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

	err := ptyRun(context.Background(), fakeCLIPath, nil, []prompt{
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
	err := ptyRun(context.Background(), fakeCLIPath, nil, nil, 500*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from timeout, got nil")
	}
	if !errors.Is(err, errPtyTimeout) {
		t.Fatalf("expected errPtyTimeout, got %v", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("took too long: %v", elapsed)
	}
}

func TestPtyRun_ContextCancellation(t *testing.T) {
	// Verify that cancelling the parent context interrupts a hung ptyRun
	// well before the hard deadline fires. Acceptance criterion for M4.
	setScenarioEnv(t, fakeScenario{
		HangForever: true,
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := ptyRun(ctx, fakeCLIPath, nil, nil, 10*time.Second)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from context cancellation, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("ptyRun did not honor context cancellation promptly: %v", elapsed)
	}
}

func TestPtyRun_NoMatchingPrompt(t *testing.T) {
	// fake-cli prints output and exits without any interactive prompts.
	setScenarioEnv(t, fakeScenario{
		FinalOutput: "Some status output\n",
	})

	err := ptyRun(context.Background(), fakeCLIPath, nil, []prompt{
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

	err := ptyRun(context.Background(), fakeCLIPath, nil, []prompt{
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

	err := ptyRun(context.Background(), fakeCLIPath, nil, nil, 2*time.Second)
	if err == nil {
		t.Fatal("expected error from non-zero exit code")
	}
}

func TestPtyRun_CarriageReturnAsEnter(t *testing.T) {
	// Verify that ptyRun sends \r (carriage return) as the line terminator, not \n.
	// Interactive CLIs like jotta-cli put stdin in raw mode, where \r is the Enter
	// key and \n is NOT treated as line submission. This test uses RawMode:true so
	// fake-cli disables ICRNL on its PTY slave and reads until \r. If ptyRun were
	// to send \n instead of \r, the fake-cli read would never terminate and the
	// test would time out.
	setScenarioEnv(t, fakeScenario{
		RawMode: true,
		Steps: []fakeStep{
			{Prompt: promptLicense, Expect: "yes"},
			{Prompt: promptToken, Expect: "tok"},
		},
		FinalOutput: "Logged in.\n",
	})

	err := ptyRun(context.Background(), fakeCLIPath, nil, []prompt{
		{promptLicense, "yes"},
		{promptToken, "tok"},
	}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPtyRun_DefersPromptResponseAfterTerminalQueries(t *testing.T) {
	setScenarioEnv(t, fakeScenario{
		RawMode: true,
		Steps: []fakeStep{
			{
				Prompt:             promptLicense,
				PromptSuffix:       queryOSC11 + queryDSR,
				ExpectQueryReplies: []string{replyDSR},
				QuietMs:            20,
				Expect:             "yes",
			},
		},
		FinalOutput: "Logged in.\n",
	})

	err := ptyRun(context.Background(), fakeCLIPath, nil, []prompt{
		{promptLicense, "yes"},
	}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPtyRun_WaitsForQuietReadBeforePromptResponse(t *testing.T) {
	setScenarioEnv(t, fakeScenario{
		RawMode: true,
		Steps: []fakeStep{
			{
				Prompt:              promptLicense,
				PromptSuffix:        queryOSC11 + queryDSR,
				PromptSuffixDelayMs: 10,
				ExpectQueryReplies:  []string{replyDSR},
				QuietMs:             20,
				Expect:              "yes",
			},
		},
		FinalOutput: "Logged in.\n",
	})

	err := ptyRun(context.Background(), fakeCLIPath, nil, []prompt{
		{promptLicense, "yes"},
	}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPtyRun_LogoutSuppressesOSC11Reply(t *testing.T) {
	setScenarioEnv(t, fakeScenario{
		RawMode: true,
		Steps: []fakeStep{
			{
				Prompt:             queryOSC11 + queryDSR + promptLogout,
				ExpectQueryReplies: []string{replyDSR},
				QuietMs:            20,
				Expect:             "y",
			},
		},
		FinalOutput: "Logged out.\n",
	})

	err := ptyRun(context.Background(), fakeCLIPath, nil, []prompt{
		{promptLogout, "y"},
	}, 5*time.Second)
	if err != nil {
		t.Fatal(err)
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

	err := loginWithTokenWithRunner(context.Background(), execRunner{}, os.Getenv)
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

	err := loginWithTokenWithRunner(context.Background(), execRunner{}, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
}

// Verify that the prompt strings in loginWithToken match our known constants.
// This test catches drift between main.go and the known prompt constants.
func TestLoginWithToken_PromptStringsMatch(t *testing.T) {
	t.Setenv("JOTTA_TOKEN", "tok")
	t.Setenv("JOTTA_DEVICE", "dev")

	// We can't easily inspect loginWithTokenWithRunner's internals, but we can verify
	// the prompt strings by running it against exact prompts. If any prompt
	// string in main.go changes, this test will hang (timeout) or fail.
	setScenarioEnv(t, fakeScenario{
		Steps: []fakeStep{
			// These must exactly match the prompts in loginWithTokenWithRunner().
			{Prompt: "accept license (yes/no): ", Expect: "yes"},
			{Prompt: "Personal login token: ", Expect: "tok"},
			{Prompt: "Device name: ", Expect: "dev"},
		},
	})

	origCLI := jottaCLI
	jottaCLI = fakeCLIPath
	defer func() { jottaCLI = origCLI }()

	err := loginWithTokenWithRunner(context.Background(), execRunner{}, os.Getenv)
	if err != nil {
		t.Fatalf("loginWithToken failed — prompt strings may have changed: %v", err)
	}
}

func TestLoginWithToken_DefersLicenseResponseAfterTerminalQueries(t *testing.T) {
	t.Setenv("JOTTA_TOKEN", "tok")
	t.Setenv("JOTTA_DEVICE", "dev")

	setScenarioEnv(t, fakeScenario{
		RawMode: true,
		Steps: []fakeStep{
			{
				Prompt:             promptLicense,
				PromptSuffix:       queryOSC11 + queryDSR,
				ExpectQueryReplies: []string{replyDSR},
				QuietMs:            20,
				Expect:             "yes",
			},
			{Prompt: promptToken, Expect: "tok"},
			{Prompt: "Device name: ", Expect: "dev"},
		},
		FinalOutput: "Login successful.\n",
	})

	origCLI := jottaCLI
	jottaCLI = fakeCLIPath
	defer func() { jottaCLI = origCLI }()

	err := loginWithTokenWithRunner(context.Background(), execRunner{}, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
}

func TestLoginWithToken_WaitsForQuietReadBeforeLicenseResponse(t *testing.T) {
	t.Setenv("JOTTA_TOKEN", "tok")
	t.Setenv("JOTTA_DEVICE", "dev")

	setScenarioEnv(t, fakeScenario{
		RawMode: true,
		Steps: []fakeStep{
			{
				Prompt:              promptLicense,
				PromptSuffix:        queryOSC11 + queryDSR,
				PromptSuffixDelayMs: 10,
				ExpectQueryReplies:  []string{replyDSR},
				QuietMs:             20,
				Expect:              "yes",
			},
			{Prompt: promptToken, Expect: "tok"},
			{Prompt: "Device name: ", Expect: "dev"},
		},
		FinalOutput: "Login successful.\n",
	})

	origCLI := jottaCLI
	jottaCLI = fakeCLIPath
	defer func() { jottaCLI = origCLI }()

	err := loginWithTokenWithRunner(context.Background(), execRunner{}, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
}

// --- Status pattern matching tests ---

func TestStatusPatternMatching(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   statusKind
	}{
		{
			name:   "matching device",
			output: "Some output\nFound remote device that matches this machine\nMore output",
			want:   statusMatchingDeviceKind,
		},
		{
			name:   "session revoked",
			output: "Error: The session has been revoked.\nPlease login again.",
			want:   statusSessionRevokedKind,
		},
		{
			name:   "device name not set",
			output: "The device name has not been set\nRun jotta-cli setup",
			want:   statusNoDeviceNameKind,
		},
		{
			name:   "not logged in",
			output: "Not logged in\nUse jotta-cli login",
			want:   statusNotLoggedInKind,
		},
		{
			name:   "device not remote",
			output: "ERROR  device [integration-test] does not exist remotely. Jottad cannot continue.",
			want:   statusDeviceMissingKind,
		},
		{
			name:   "unknown status",
			output: "Something unexpected happened",
			want:   statusUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyStatus(tt.output)
			if got != tt.want {
				t.Errorf("classifyStatus(%q) = %q, want %q", tt.output, got, tt.want)
			}
		})
	}
}

// --- loadEnvFile tests ---

func TestLoadEnvFile_BasicKeyValue(t *testing.T) {
	f := writeTempFile(t, "KEY1=value1\nKEY2=value2\n")
	loadEnvFile(f, os.Setenv, io.Discard)
	assertEnv(t, "KEY1", "value1")
	assertEnv(t, "KEY2", "value2")
}

func TestLoadEnvFile_QuotedValues(t *testing.T) {
	f := writeTempFile(t, `DOUBLE="hello world"`+"\n"+`SINGLE='foo bar'`+"\n")
	loadEnvFile(f, os.Setenv, io.Discard)
	assertEnv(t, "DOUBLE", "hello world")
	assertEnv(t, "SINGLE", "foo bar")
}

func TestLoadEnvFile_ExportPrefix(t *testing.T) {
	f := writeTempFile(t, "export MY_VAR=exported\n")
	loadEnvFile(f, os.Setenv, io.Discard)
	assertEnv(t, "MY_VAR", "exported")
}

func TestLoadEnvFile_CommentsAndBlanks(t *testing.T) {
	f := writeTempFile(t, "# comment\n\nVALID=yes\n  # indented comment\n")
	loadEnvFile(f, os.Setenv, io.Discard)
	assertEnv(t, "VALID", "yes")
}

func TestLoadEnvFile_NoEquals(t *testing.T) {
	f := writeTempFile(t, "NOEQUALS\n=noleft\nGOOD=ok\n")
	loadEnvFile(f, os.Setenv, io.Discard)
	assertEnv(t, "GOOD", "ok")
}

func TestLoadEnvFile_MissingFile(t *testing.T) {
	// Should not panic or error — silently ignored.
	loadEnvFile("/nonexistent/path/env", os.Setenv, io.Discard)
}

func TestLoadEnvFile_WarnsOnScannerError(t *testing.T) {
	// A single line longer than bufio.MaxScanTokenSize (64 KiB) causes
	// scanner.Err() to return bufio.ErrTooLong. Verify the warning reaches
	// stderr instead of being silently dropped.
	longValue := strings.Repeat("A", 70000)
	f := writeTempFile(t, "LONG="+longValue+"\n")
	var buf bytes.Buffer
	loadEnvFile(f, func(string, string) error { return nil }, &buf)
	if !strings.Contains(buf.String(), "warning:") {
		t.Fatalf("expected scanner-error warning on stderr, got %q", buf.String())
	}
}

func TestLoadEnvFile_WarnsOnSetenvError(t *testing.T) {
	f := writeTempFile(t, "OK=value\n")
	var buf bytes.Buffer
	setErr := errors.New("mock setenv failure")
	loadEnvFile(f, func(string, string) error { return setErr }, &buf)
	if !strings.Contains(buf.String(), "mock setenv failure") {
		t.Fatalf("expected setenv error on stderr, got %q", buf.String())
	}
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

// --- envDurationSecondsFrom tests ---

func TestEnvDurationSecondsFrom(t *testing.T) {
	tests := []struct {
		name string
		val  string
		def  time.Duration
		want time.Duration
	}{
		{"unset", "", 5 * time.Second, 5 * time.Second},
		{"valid", "30", 5 * time.Second, 30 * time.Second},
		{"zero returns default", "0", 5 * time.Second, 5 * time.Second},
		{"negative returns default", "-5", 5 * time.Second, 5 * time.Second},
		{"invalid returns default", "abc", 7 * time.Second, 7 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(string) string { return tt.val }
			if got := envDurationSecondsFrom(getenv, "KEY", tt.def); got != tt.want {
				t.Errorf("envDurationSecondsFrom = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- forceSymlink tests ---

func TestForceSymlink_New(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	mkdirAll(t, target)
	link := filepath.Join(dir, "link")

	if err := forceSymlink(target, link); err != nil {
		t.Fatal(err)
	}

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
	mkdirAll(t, target1)
	mkdirAll(t, target2)
	link := filepath.Join(dir, "link")

	if err := os.Symlink(target1, link); err != nil {
		t.Fatal(err)
	}
	if err := forceSymlink(target2, link); err != nil {
		t.Fatal(err)
	}

	resolved, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != target2 {
		t.Errorf("symlink points to %q, want %q", resolved, target2)
	}
}

func TestForceSymlink_RefusesExistingDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	mkdirAll(t, target)
	link := filepath.Join(dir, "link")
	mkdirAll(t, link)

	err := forceSymlink(target, link)
	if err == nil {
		t.Fatal("expected error when link path is an existing directory")
	}
	if !strings.Contains(err.Error(), "refusing to replace") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestForceSymlink_ReplacesRegularFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	mkdirAll(t, target)
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(link, []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := forceSymlink(target, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != target {
		t.Errorf("symlink points to %q, want %q", resolved, target)
	}
}

func TestPreparePersistentPaths_CreatesManagedFiles(t *testing.T) {
	withManagedPaths(t)

	if err := preparePersistentPaths(); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, configFilePath); got != defaultConfigFileContent() {
		t.Fatalf("config template mismatch:\n%s", got)
	}
	if got := readFile(t, ignoreFilePath); got != defaultIgnoreFileContent() {
		t.Fatalf("ignore template mismatch:\n%s", got)
	}
}

func TestPreparePersistentPaths_PreservesExistingManagedFiles(t *testing.T) {
	withManagedPaths(t)

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configFilePath, []byte("custom=1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ignoreFilePath, []byte("custom/pattern\n"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := preparePersistentPaths(); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, configFilePath); got != "custom=1\n" {
		t.Fatalf("config file overwritten: %q", got)
	}
	if got := readFile(t, ignoreFilePath); got != "custom/pattern\n" {
		t.Fatalf("ignore file overwritten: %q", got)
	}
}

// --- configureLocaltime tests ---

func TestConfigureLocaltime_Empty(t *testing.T) {
	if err := configureLocaltime(""); err != nil {
		t.Fatalf("empty localtime should be no-op, got %v", err)
	}
}

func TestConfigureLocaltime_TraversalRejected(t *testing.T) {
	err := configureLocaltime("../etc/passwd")
	if err == nil || !strings.Contains(err.Error(), "invalid LOCALTIME") {
		t.Fatalf("expected invalid LOCALTIME error, got %v", err)
	}
}

func TestConfigureLocaltime_MissingZone(t *testing.T) {
	err := configureLocaltime("Nowhere/Nope")
	if err == nil || !strings.Contains(err.Error(), "invalid LOCALTIME") {
		t.Fatalf("expected invalid LOCALTIME error, got %v", err)
	}
}

// --- loginWithToken missing-env tests ---

func TestLoginWithTokenWithRunner_MissingToken(t *testing.T) {
	runner := &fakeRunner{}
	err := loginWithTokenWithRunner(context.Background(), runner, envMap("JOTTA_DEVICE", "dev"))
	if err == nil || !strings.Contains(err.Error(), "JOTTA_TOKEN") {
		t.Fatalf("expected missing JOTTA_TOKEN error, got %v", err)
	}
}

func TestLoginWithTokenWithRunner_MissingDevice(t *testing.T) {
	runner := &fakeRunner{}
	err := loginWithTokenWithRunner(context.Background(), runner, envMap("JOTTA_TOKEN", "tok"))
	if err == nil || !strings.Contains(err.Error(), "JOTTA_DEVICE") {
		t.Fatalf("expected missing JOTTA_DEVICE error, got %v", err)
	}
}

// --- terminateProcess tests ---

func TestTerminateProcess_SignalsAndExits(t *testing.T) {
	done := make(chan error, 1)
	p := &signalingProcess{
		onSignal: func() { done <- nil; close(done) },
	}

	terminateProcess(asyncProcess{proc: p, done: done}, time.Second)
	if p.sigCount != 1 {
		t.Fatalf("expected one SIGTERM, got %d", p.sigCount)
	}
	if p.killed {
		t.Fatal("did not expect Kill when process exits gracefully")
	}
}

func TestTerminateProcess_EscalatesToKillOnTimeout(t *testing.T) {
	done := make(chan error, 1)
	p := &signalingProcess{
		onKill: func() { done <- nil; close(done) },
	}

	terminateProcess(asyncProcess{proc: p, done: done}, 20*time.Millisecond)
	if !p.killed {
		t.Fatal("expected Kill after grace period elapsed")
	}
}

func TestTerminateProcess_AlreadyExited(t *testing.T) {
	done := make(chan error, 1)
	close(done)
	p := &signalingProcess{}
	terminateProcess(asyncProcess{proc: p, done: done}, time.Second)
	if p.sigCount != 0 || p.killed {
		t.Fatal("should not signal an already-exited process")
	}
}

type signalingProcess struct {
	sigCount int
	killed   bool
	onSignal func()
	onKill   func()
}

func (p *signalingProcess) Wait() error { return nil }
func (p *signalingProcess) Signal(os.Signal) error {
	p.sigCount++
	if p.onSignal != nil {
		p.onSignal()
	}
	return nil
}
func (p *signalingProcess) Kill() error {
	p.killed = true
	if p.onKill != nil {
		p.onKill()
	}
	return nil
}

// --- helpers ---

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "env-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
}

func withManagedPaths(t *testing.T) {
	t.Helper()
	baseDir := t.TempDir()

	oldDataDir := dataDir
	oldConfigDir := configDir
	oldConfigFilePath := configFilePath
	oldIgnoreFilePath := ignoreFilePath
	oldRootJottadPath := rootJottadPath
	oldRootJottaCLIConfigDir := rootJottaCLIConfigDir
	oldSyncRootMountPath := syncRootMountPath

	dataDir = filepath.Join(baseDir, "data", "jottad")
	configDir = filepath.Join(dataDir, "jotta-cli")
	configFilePath = filepath.Join(dataDir, "jotta-config.env")
	ignoreFilePath = filepath.Join(dataDir, "ignorefile")
	rootJottadPath = filepath.Join(baseDir, "root", ".jottad")
	rootJottaCLIConfigDir = filepath.Join(baseDir, "root", ".config", "jotta-cli")
	syncRootMountPath = filepath.Join(baseDir, "sync")

	t.Cleanup(func() {
		dataDir = oldDataDir
		configDir = oldConfigDir
		configFilePath = oldConfigFilePath
		ignoreFilePath = oldIgnoreFilePath
		rootJottadPath = oldRootJottadPath
		rootJottaCLIConfigDir = oldRootJottaCLIConfigDir
		syncRootMountPath = oldSyncRootMountPath
	})

}

func assertEnv(t *testing.T, key, want string) {
	t.Helper()
	if got := os.Getenv(key); got != want {
		t.Errorf("env %s = %q, want %q", key, got, want)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func countCalls(calls []string, want string) int {
	count := 0
	for _, call := range calls {
		if call == want {
			count++
		}
	}
	return count
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestTerminalResponder_SplitQueriesAcrossReads(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()

	var responder terminalResponder
	got, err := responder.respond(writer, "\x1b]11;")
	if err != nil {
		t.Fatalf("respond returned error: %v", err)
	}
	if got {
		t.Fatal("unexpected reply for incomplete OSC11 query")
	}
	got, err = responder.respond(writer, "?\x1b\\hello\x1b[")
	if err != nil {
		t.Fatalf("respond returned error: %v", err)
	}
	if !got {
		t.Fatal("expected OSC11 reply after completed split query")
	}
	got, err = responder.respond(writer, "6n")
	if err != nil {
		t.Fatalf("respond returned error: %v", err)
	}
	if !got {
		t.Fatal("expected DSR reply after completed split query")
	}

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	reply, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if string(reply) != replyOSC11+replyDSR {
		t.Fatalf("terminal replies = %q, want %q", string(reply), replyOSC11+replyDSR)
	}
}

func TestWaitForStartup_SessionRevokedLogsOutAndBackIn(t *testing.T) {
	runner := &fakeRunner{
		statusResults: []fakeCmdResult{
			{output: statusSessionRevoked, err: errors.New("timeout")},
			{output: "ready", err: nil},
		},
	}
	var stdout bytes.Buffer
	a := app{
		runner:          runner,
		stdout:          &stdout,
		stderr:          io.Discard,
		sleep:           func(time.Duration) {},
		getenv:          envMap("JOTTA_TOKEN", "tok", "JOTTA_DEVICE", "dev"),
		monitorInterval: time.Millisecond,
	}

	if err := a.waitForStartup(context.Background()); err != nil {
		t.Fatal(err)
	}

	if !runner.called("pty " + cmdKey(jottaCLI, []string{"logout"})) {
		t.Fatal("expected logout PTY call")
	}
	if !runner.called("pty " + cmdKey(jottaCLI, []string{"login"})) {
		t.Fatal("expected login PTY call")
	}
}

func TestWaitForStartup_CanceledContextReturnsNil(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	a := app{
		runner:          &fakeRunner{},
		stdout:          io.Discard,
		stderr:          io.Discard,
		sleep:           func(time.Duration) {},
		getenv:          os.Getenv,
		monitorInterval: time.Millisecond,
	}

	if err := a.waitForStartup(ctx); err != nil {
		t.Fatalf("waitForStartup error = %v, want nil", err)
	}
}

func TestWaitForStartup_UnknownStatusTimesOutWithDiagnostic(t *testing.T) {
	t.Setenv("STARTUP_TIMEOUT", "1")

	runner := &fakeRunner{
		statusResults: []fakeCmdResult{
			{output: "still booting", err: errors.New("timeout")},
		},
		runResults: map[string][]fakeCmdResult{
			cmdKey(jottaCLI, []string{"status"}): {
				{output: "diagnostic output", err: nil},
			},
		},
	}
	var stdout bytes.Buffer
	a := app{
		runner:          runner,
		stdout:          &stdout,
		stderr:          io.Discard,
		sleep:           func(time.Duration) {},
		getenv:          os.Getenv,
		monitorInterval: time.Millisecond,
	}

	err := a.waitForStartup(context.Background())
	if err == nil || !strings.Contains(err.Error(), "startup timeout") {
		t.Fatalf("waitForStartup error = %v, want startup timeout", err)
	}
	if !strings.Contains(stdout.String(), "diagnostic output") {
		t.Fatalf("expected diagnostic output in stdout, got %q", stdout.String())
	}
}

func TestEnsureSyncConfigured_ContinuesOnStatusProbeError(t *testing.T) {
	runner := &fakeRunner{
		statusResults: []fakeCmdResult{
			{output: "daemon busy", err: errors.New("timeout")},
		},
	}
	var stdout bytes.Buffer
	a := app{
		runner:          runner,
		stdout:          &stdout,
		stderr:          io.Discard,
		sleep:           func(time.Duration) {},
		getenv:          os.Getenv,
		monitorInterval: time.Millisecond,
	}

	if err := a.ensureSyncConfigured(context.Background()); err != nil {
		t.Fatalf("ensureSyncConfigured error = %v, want nil", err)
	}
	if runner.called("pty " + cmdKey(jottaCLI, []string{"sync", "setup", "--root", syncRootMountPath})) {
		t.Fatal("did not expect sync setup PTY call")
	}
	if !strings.Contains(stdout.String(), "sync status probe failed") {
		t.Fatalf("expected warning in stdout, got %q", stdout.String())
	}
}

func TestEnsureSyncConfigured_SetsUpWhenSyncDisabled(t *testing.T) {
	runner := &fakeRunner{
		statusResults: []fakeCmdResult{
			{output: statusSyncDisabled, err: errors.New("exit status 1")},
		},
	}
	a := app{
		runner:          runner,
		stdout:          io.Discard,
		stderr:          io.Discard,
		sleep:           func(time.Duration) {},
		getenv:          os.Getenv,
		monitorInterval: time.Millisecond,
	}

	if err := a.ensureSyncConfigured(context.Background()); err != nil {
		t.Fatalf("ensureSyncConfigured error = %v, want nil", err)
	}
	if !runner.called("pty " + cmdKey(jottaCLI, []string{"sync", "setup", "--root", syncRootMountPath})) {
		t.Fatal("expected sync setup PTY call")
	}
}

func TestApplyManagedConfig_FailsOnCommandError(t *testing.T) {
	runner := &fakeRunner{
		runResults: map[string][]fakeCmdResult{
			cmdKey(jottaCLI, []string{"config", "scaninterval", "1m"}): {
				{output: "bad config", err: errors.New("exit status 2")},
			},
		},
	}
	tmpDir := t.TempDir()
	oldPath := managedConfigStatePath
	managedConfigStatePath = filepath.Join(tmpDir, "managed-config.state")
	t.Cleanup(func() { managedConfigStatePath = oldPath })

	a := app{
		runner:          runner,
		stdout:          io.Discard,
		stderr:          io.Discard,
		sleep:           func(time.Duration) {},
		getenv:          func(string) string { return "" },
		environ:         func() []string { return []string{"JOTTA_CONFIG_SCANINTERVAL=1m"} },
		monitorInterval: time.Millisecond,
	}

	err := a.applyManagedConfig(context.Background())
	if err == nil || !strings.Contains(err.Error(), "bad config") {
		t.Fatalf("applyManagedConfig error = %v, want command failure", err)
	}
}

func TestConfigureBackups_SkipsAlreadyAdded(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backup", "already")
	mkdirAll(t, backupDir)

	runner := &fakeRunner{
		runResults: map[string][]fakeCmdResult{
			cmdKey(jottaCLI, []string{"add", backupDir}): {
				{output: "path already added to backup", err: errors.New("exit status 1")},
			},
		},
	}
	slept := false
	a := app{
		runner:          runner,
		stdout:          io.Discard,
		stderr:          io.Discard,
		sleep:           func(time.Duration) { slept = true },
		getenv:          os.Getenv,
		monitorInterval: time.Millisecond,
	}

	if err := a.configureBackupsIn(context.Background(), filepath.Join(dir, "backup", "*")); err != nil {
		t.Fatalf("configureBackups error = %v, want nil", err)
	}
	if slept {
		t.Fatal("should not sleep when no new directories were added")
	}
}

func TestConfigureBackups_NewDirTriggersSettle(t *testing.T) {
	dir := t.TempDir()
	backupDir := filepath.Join(dir, "backup", "new")
	mkdirAll(t, backupDir)

	runner := &fakeRunner{}
	slept := false
	a := app{
		runner:          runner,
		stdout:          io.Discard,
		stderr:          io.Discard,
		sleep:           func(time.Duration) { slept = true },
		getenv:          os.Getenv,
		monitorInterval: time.Millisecond,
	}

	if err := a.configureBackupsIn(context.Background(), filepath.Join(dir, "backup", "*")); err != nil {
		t.Fatalf("configureBackups error = %v, want nil", err)
	}
	if !slept {
		t.Fatal("expected settle delay after adding a new directory")
	}
}

func TestDesiredIgnorePatterns_MergesFileAndEnvPatterns(t *testing.T) {
	withManagedPaths(t)
	if err := os.MkdirAll(filepath.Dir(ignoreFilePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ignoreFilePath, []byte("# comment\nbase/pattern\n# commented\n"), 0644); err != nil {
		t.Fatal(err)
	}

	a := app{
		getenv: func(key string) string {
			if key == "JOTTA_IGNORE_PATTERNS" {
				return "extra/one,extra/two"
			}
			return ""
		},
	}

	patterns, err := a.desiredIgnorePatterns()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"base/pattern", "extra/one", "extra/two"} {
		if !containsString(patterns, want) {
			t.Fatalf("desiredIgnorePatterns missing %q", want)
		}
	}
	if containsString(patterns, defaultIgnorePatterns[0]) {
		t.Fatalf("desiredIgnorePatterns unexpectedly injected built-in default %q", defaultIgnorePatterns[0])
	}
}

func setupIgnoreTest(t *testing.T, ignoreFileContent string, stateContent string) *fakeRunner {
	t.Helper()
	withManagedPaths(t)

	oldState := managedIgnoreStatePath
	managedIgnoreStatePath = filepath.Join(filepath.Dir(configFilePath), "managed-ignores.state")
	t.Cleanup(func() { managedIgnoreStatePath = oldState })

	if err := os.MkdirAll(filepath.Dir(ignoreFilePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ignoreFilePath, []byte(ignoreFileContent), 0644); err != nil {
		t.Fatal(err)
	}
	if stateContent != "" {
		if err := os.WriteFile(managedIgnoreStatePath, []byte(stateContent), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return &fakeRunner{}
}

func TestApplyManagedIgnores_AddsNewPatterns(t *testing.T) {
	runner := setupIgnoreTest(t, "a/new\nb/new\n", "")
	a := app{
		runner: runner,
		stdout: io.Discard,
		stderr: io.Discard,
		getenv: func(string) string { return "" },
	}

	if err := a.applyManagedIgnores(context.Background()); err != nil {
		t.Fatalf("applyManagedIgnores error = %v, want nil", err)
	}
	if !runner.called("run " + cmdKey(jottaCLI, []string{"ignores", "add", "--pattern", "a/new"})) {
		t.Errorf("expected ignores add for a/new, got calls: %v", runner.calls)
	}
	if !runner.called("run " + cmdKey(jottaCLI, []string{"ignores", "add", "--pattern", "b/new"})) {
		t.Errorf("expected ignores add for b/new, got calls: %v", runner.calls)
	}

	persisted, err := readStateLines(managedIgnoreStatePath)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a/new", "b/new"}
	if !stringSlicesEqual(persisted, want) {
		t.Fatalf("persisted state = %v, want %v", persisted, want)
	}
}

func TestApplyManagedIgnores_RetriesTransientContextDeadline(t *testing.T) {
	runner := setupIgnoreTest(t, "flaky/pattern\n", "")
	runner.statusResults = []fakeCmdResult{
		{output: "ready", err: nil},
		{output: "ready", err: nil},
	}
	runner.runResults = map[string][]fakeCmdResult{
		cmdKey(jottaCLI, []string{"ignores", "add", "--pattern", "flaky/pattern"}): {
			{output: "ERROR  context deadline exceeded", err: errors.New("exit status 1")},
			{output: "", err: nil},
		},
	}
	a := app{
		runner: runner,
		stdout: io.Discard,
		stderr: io.Discard,
		sleep:  func(time.Duration) {},
		getenv: envMap("BOOTSTRAP_TIMEOUT", "5"),
	}

	if err := a.applyManagedIgnores(context.Background()); err != nil {
		t.Fatalf("applyManagedIgnores error = %v, want nil", err)
	}
	runCall := "run " + cmdKey(jottaCLI, []string{"ignores", "add", "--pattern", "flaky/pattern"})
	if got := countCalls(runner.calls, runCall); got != 2 {
		t.Fatalf("ignore add attempts = %d, want 2; calls=%v", got, runner.calls)
	}
	persisted, err := readStateLines(managedIgnoreStatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !stringSlicesEqual(persisted, []string{"flaky/pattern"}) {
		t.Fatalf("persisted state = %v, want [flaky/pattern]", persisted)
	}
}

func TestRunCheckedRetry_DoesNotRetryPermanentError(t *testing.T) {
	runner := &fakeRunner{
		statusResults: []fakeCmdResult{{output: "ready", err: nil}},
		runResults: map[string][]fakeCmdResult{
			cmdKey(jottaCLI, []string{"config", "scaninterval", "bad"}): {
				{output: "bad config", err: errors.New("exit status 2")},
			},
		},
	}
	a := app{
		runner: runner,
		stdout: io.Discard,
		stderr: io.Discard,
		sleep:  func(time.Duration) {},
		getenv: envMap("BOOTSTRAP_TIMEOUT", "5"),
	}

	_, err := a.runCheckedRetry(context.Background(), "set config", jottaCLI, "config", "scaninterval", "bad")
	if err == nil || !strings.Contains(err.Error(), "bad config") {
		t.Fatalf("runCheckedRetry error = %v, want permanent command error", err)
	}
	runCall := "run " + cmdKey(jottaCLI, []string{"config", "scaninterval", "bad"})
	if got := countCalls(runner.calls, runCall); got != 1 {
		t.Fatalf("config attempts = %d, want 1; calls=%v", got, runner.calls)
	}
}

func TestRunCheckedRetry_ProbesStatusBeforeCommand(t *testing.T) {
	runner := &fakeRunner{
		statusResults: []fakeCmdResult{{output: "ready", err: nil}},
	}
	a := app{
		runner: runner,
		stdout: io.Discard,
		stderr: io.Discard,
		sleep:  func(time.Duration) {},
		getenv: envMap("BOOTSTRAP_TIMEOUT", "5"),
	}

	if _, err := a.runCheckedRetry(context.Background(), "add backup directory", jottaCLI, "add", "/backup/data"); err != nil {
		t.Fatal(err)
	}
	wantRun := "run " + cmdKey(jottaCLI, []string{"add", "/backup/data"})
	if len(runner.calls) < 2 || runner.calls[0] != "status" || runner.calls[1] != wantRun {
		t.Fatalf("calls = %v, want status before %q", runner.calls, wantRun)
	}
}

func TestApplyManagedIgnores_RemovesStalePatterns(t *testing.T) {
	runner := setupIgnoreTest(t, "keep/me\n", "drop/me\nkeep/me\n")
	a := app{
		runner: runner,
		stdout: io.Discard,
		stderr: io.Discard,
		getenv: func(string) string { return "" },
	}

	if err := a.applyManagedIgnores(context.Background()); err != nil {
		t.Fatalf("applyManagedIgnores error = %v, want nil", err)
	}
	if !runner.called("run " + cmdKey(jottaCLI, []string{"ignores", "rem", "--pattern", "drop/me"})) {
		t.Errorf("expected ignores rem for drop/me, got calls: %v", runner.calls)
	}
	if runner.called("run " + cmdKey(jottaCLI, []string{"ignores", "add", "--pattern", "keep/me"})) {
		t.Errorf("unexpected re-add of already-tracked pattern keep/me")
	}

	persisted, err := readStateLines(managedIgnoreStatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !stringSlicesEqual(persisted, []string{"keep/me"}) {
		t.Fatalf("persisted state = %v, want [keep/me]", persisted)
	}
}

func TestApplyManagedIgnores_NoOpWhenStateMatches(t *testing.T) {
	runner := setupIgnoreTest(t, "a/x\nb/y\n", "a/x\nb/y\n")
	a := app{
		runner: runner,
		stdout: io.Discard,
		stderr: io.Discard,
		getenv: func(string) string { return "" },
	}

	if err := a.applyManagedIgnores(context.Background()); err != nil {
		t.Fatalf("applyManagedIgnores error = %v, want nil", err)
	}
	for _, call := range runner.calls {
		if strings.Contains(call, "ignores add") || strings.Contains(call, "ignores rem") {
			t.Fatalf("expected no ignores mutations when state matches, got %q", call)
		}
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDesiredConfigSettings_MergesFileAndEnvOverrides(t *testing.T) {
	withManagedPaths(t)
	if err := os.MkdirAll(filepath.Dir(configFilePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configFilePath, []byte("maxuploads=3\n# comment\nignorehiddenfiles=false\n# maxdownloads=9\n"), 0644); err != nil {
		t.Fatal(err)
	}

	a := app{
		getenv: func(string) string { return "" },
		environ: func() []string {
			return []string{
				"JOTTA_CONFIG_MAXDOWNLOADS=4",
				"JOTTA_CONFIG_SCANINTERVAL=30m",
				"JOTTA_CONFIG_IGNOREHIDDENFILES=true",
			}
		},
	}

	got, err := a.desiredConfigSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got["maxuploads"] != "3" {
		t.Fatalf("maxuploads = %q, want 3", got["maxuploads"])
	}
	if got["maxdownloads"] != "4" {
		t.Fatalf("maxdownloads = %q, want 4", got["maxdownloads"])
	}
	if got["scaninterval"] != "30m" {
		t.Fatalf("scaninterval = %q, want 30m", got["scaninterval"])
	}
	if got["ignorehiddenfiles"] != "true" {
		t.Fatalf("ignorehiddenfiles = %q, want true (env override should win)", got["ignorehiddenfiles"])
	}
}

func TestApplyManagedConfig_ResetsUnsetKeyToDefault(t *testing.T) {
	tmpDir := t.TempDir()
	oldPath := managedConfigStatePath
	managedConfigStatePath = filepath.Join(tmpDir, "managed-config.state")
	t.Cleanup(func() { managedConfigStatePath = oldPath })
	if err := os.WriteFile(managedConfigStatePath, []byte("scaninterval=15m\n"), 0644); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{runResults: map[string][]fakeCmdResult{
		cmdKey(jottaCLI, []string{"config", "scaninterval", "1h0m0s"}): {
			{output: "", err: nil},
		},
	}}
	a := app{
		runner:  runner,
		stdout:  io.Discard,
		stderr:  io.Discard,
		sleep:   func(time.Duration) {},
		getenv:  func(string) string { return "" },
		environ: func() []string { return nil },
	}

	if err := a.applyManagedConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !runner.called("run " + cmdKey(jottaCLI, []string{"config", "scaninterval", "1h0m0s"})) {
		t.Fatal("expected scaninterval reset to default")
	}
}

func TestApplyManagedConfig_ResetsMaxTransfersToCurrentDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	oldPath := managedConfigStatePath
	managedConfigStatePath = filepath.Join(tmpDir, "managed-config.state")
	t.Cleanup(func() { managedConfigStatePath = oldPath })
	if err := os.WriteFile(managedConfigStatePath, []byte("maxuploads=3\nmaxdownloads=4\n"), 0644); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{runResults: map[string][]fakeCmdResult{
		cmdKey(jottaCLI, []string{"config", "maxuploads", "12"}): {
			{output: "", err: nil},
		},
		cmdKey(jottaCLI, []string{"config", "maxdownloads", "12"}): {
			{output: "", err: nil},
		},
	}}
	a := app{
		runner:  runner,
		stdout:  io.Discard,
		stderr:  io.Discard,
		sleep:   func(time.Duration) {},
		getenv:  func(string) string { return "" },
		environ: func() []string { return nil },
	}

	if err := a.applyManagedConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !runner.called("run " + cmdKey(jottaCLI, []string{"config", "maxuploads", "12"})) {
		t.Fatal("expected maxuploads reset to current default")
	}
	if !runner.called("run " + cmdKey(jottaCLI, []string{"config", "maxdownloads", "12"})) {
		t.Fatal("expected maxdownloads reset to current default")
	}
}

func TestConfigureSync_EmptyMountedDirectoryTriggersSetup(t *testing.T) {
	withManagedPaths(t)
	if err := os.MkdirAll(syncRootMountPath, 0755); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		statusResults: []fakeCmdResult{
			{output: statusSyncDisabled, err: errors.New("exit status 1")},
			{output: statusSyncDisabled, err: errors.New("exit status 1")},
		},
	}
	a := app{
		runner:          runner,
		stdout:          io.Discard,
		stderr:          io.Discard,
		sleep:           func(time.Duration) {},
		getenv:          os.Getenv,
		monitorInterval: time.Millisecond,
	}

	if err := a.configureSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !runner.called("pty " + cmdKey(jottaCLI, []string{"sync", "setup", "--root", syncRootMountPath})) {
		t.Fatal("expected sync setup for empty mounted directory")
	}
	if !runner.called("run " + cmdKey(jottaCLI, []string{"sync", "start"})) {
		t.Fatal("expected sync start after setup")
	}
	if got := readFile(t, syncRootStatePath()); strings.TrimSpace(got) != syncRootMountPath {
		t.Fatalf("persisted sync root = %q, want %q", got, syncRootMountPath)
	}
}

func TestConfigureSync_MissingMountSkipsWithoutState(t *testing.T) {
	withManagedPaths(t)

	a := app{
		runner:          &fakeRunner{},
		stdout:          io.Discard,
		stderr:          io.Discard,
		sleep:           func(time.Duration) {},
		getenv:          os.Getenv,
		monitorInterval: time.Millisecond,
	}

	if err := a.configureSync(context.Background()); err != nil {
		t.Fatalf("configureSync error = %v, want nil", err)
	}
}

func TestConfigureSync_MissingMountFailsWhenStateExists(t *testing.T) {
	withManagedPaths(t)
	if err := os.MkdirAll(filepath.Dir(syncRootStatePath()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(syncRootStatePath(), []byte("/previous-sync\n"), 0644); err != nil {
		t.Fatal(err)
	}

	a := app{
		runner:          &fakeRunner{},
		stdout:          io.Discard,
		stderr:          io.Discard,
		sleep:           func(time.Duration) {},
		getenv:          os.Getenv,
		monitorInterval: time.Millisecond,
	}

	err := a.configureSync(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not mounted") {
		t.Fatalf("configureSync error = %v, want missing mount error", err)
	}
}

func TestConfigureSync_ReconfiguresMismatchedRoot(t *testing.T) {
	withManagedPaths(t)
	if err := os.MkdirAll(filepath.Dir(syncRootStatePath()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(syncRootStatePath(), []byte("/old-sync\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(syncRootMountPath, 0755); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		statusResults: []fakeCmdResult{
			{output: statusSyncDisabled, err: errors.New("exit status 1")},
			{output: statusSyncDisabled, err: errors.New("exit status 1")},
		},
	}
	var stdout bytes.Buffer
	a := app{
		runner:          runner,
		stdout:          &stdout,
		stderr:          io.Discard,
		sleep:           func(time.Duration) {},
		getenv:          os.Getenv,
		monitorInterval: time.Millisecond,
	}

	if err := a.configureSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !runner.called("run " + cmdKey(jottaCLI, []string{"sync", "reset"})) {
		t.Fatal("expected sync reset before reconfiguration")
	}
	if !runner.called("pty " + cmdKey(jottaCLI, []string{"sync", "setup", "--root", syncRootMountPath})) {
		t.Fatal("expected sync setup for canonical mount path")
	}
	if !strings.Contains(stdout.String(), "Sync root changed from /old-sync") {
		t.Fatalf("expected sync reconfiguration log, got %q", stdout.String())
	}
}

func TestConfigureSync_ExistingCanonicalRootStartsWithoutSetup(t *testing.T) {
	withManagedPaths(t)
	if err := os.MkdirAll(filepath.Dir(syncRootStatePath()), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(syncRootStatePath(), []byte(syncRootMountPath+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(syncRootMountPath, 0755); err != nil {
		t.Fatal(err)
	}

	runner := &fakeRunner{
		statusResults: []fakeCmdResult{
			{output: "ready", err: nil},
		},
	}
	a := app{
		runner:          runner,
		stdout:          io.Discard,
		stderr:          io.Discard,
		sleep:           func(time.Duration) {},
		getenv:          os.Getenv,
		monitorInterval: time.Millisecond,
	}

	if err := a.configureSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runner.called("pty " + cmdKey(jottaCLI, []string{"sync", "setup", "--root", syncRootMountPath})) {
		t.Fatal("did not expect sync setup for canonical persisted root")
	}
	if !runner.called("run " + cmdKey(jottaCLI, []string{"sync", "start"})) {
		t.Fatal("expected sync start")
	}
	if got := readFile(t, syncRootStatePath()); strings.TrimSpace(got) != syncRootMountPath {
		t.Fatalf("persisted sync root = %q, want %q", got, syncRootMountPath)
	}
}

func TestRotatingFileWriter_AppendsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "container.log")

	w, err := newRotatingFileWriter(path, 100, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	w, err = newRotatingFileWriter(path, 100, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(" world")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if got := readFile(t, path); got != "hello world" {
		t.Fatalf("log content = %q, want %q", got, "hello world")
	}
}

func TestRotatingFileWriter_RotatesAndCapsBackups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "container.log")
	w, err := newRotatingFileWriter(path, 10, 4)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 7; i++ {
		if _, err := fmt.Fprintf(w, "%09d\n", i); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected active log file: %v", err)
	}
	for i := 1; i <= 4; i++ {
		if _, err := os.Stat(fmt.Sprintf("%s.%d", path, i)); err != nil {
			t.Fatalf("expected rotated log %d: %v", i, err)
		}
	}
	if _, err := os.Stat(path + ".5"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected oldest rotated log to be discarded, got err=%v", err)
	}
	if got := strings.TrimSpace(readFile(t, path)); got != "000000006" {
		t.Fatalf("active log = %q, want latest record", got)
	}
}

func TestHealthcheck_Healthy(t *testing.T) {
	a := app{
		runner: &fakeRunner{
			statusResults: []fakeCmdResult{{output: "ready", err: nil}},
		},
	}

	if err := a.healthcheck(); err != nil {
		t.Fatalf("healthcheck error = %v, want nil", err)
	}
}

func TestRun_HealthcheckPreparesPersistentPaths(t *testing.T) {
	withManagedPaths(t)

	a := app{
		runner: &fakeRunner{
			statusResults: []fakeCmdResult{{output: "ready", err: nil}},
		},
		stdout:          io.Discard,
		stderr:          io.Discard,
		sleep:           func(time.Duration) {},
		getenv:          envMap("LOCALTIME", ""),
		environ:         func() []string { return nil },
		setenv:          os.Setenv,
		monitorInterval: time.Millisecond,
	}

	if err := a.run(context.Background(), []string{"healthcheck"}); err != nil {
		t.Fatalf("run healthcheck error = %v, want nil", err)
	}

	if got := readFile(t, configFilePath); !strings.Contains(got, "# maxuploads=12") {
		t.Fatalf("config file = %q, want current default maxuploads template", got)
	}
	if _, err := os.Stat(ignoreFilePath); err != nil {
		t.Fatalf("expected ignore file to be prepared: %v", err)
	}
	if target, err := os.Readlink(rootJottadPath); err != nil || target != dataDir {
		t.Fatalf("root jottad symlink = %q, %v, want %q", target, err, dataDir)
	}
	if target, err := os.Readlink(rootJottaCLIConfigDir); err != nil || target != configDir {
		t.Fatalf("root config symlink = %q, %v, want %q", target, err, configDir)
	}
}

func TestHealthcheck_FatalStatuses(t *testing.T) {
	tests := []struct {
		name   string
		output string
	}{
		{name: "not logged in", output: statusNotLoggedIn},
		{name: "session revoked", output: statusSessionRevoked},
		{name: "device missing", output: statusDeviceMissing},
		{name: "device name missing", output: statusNoDeviceName},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := app{
				runner: &fakeRunner{
					statusResults: []fakeCmdResult{{output: tt.output, err: errors.New("exit status 1")}},
				},
			}
			if err := a.healthcheck(); err == nil || !strings.Contains(err.Error(), "unhealthy status") {
				t.Fatalf("healthcheck error = %v, want unhealthy status", err)
			}
		})
	}
}

func TestHealthcheck_TimeoutFailure(t *testing.T) {
	a := app{
		runner: &fakeRunner{
			statusResults: []fakeCmdResult{{output: "", err: errStatusTimeout}},
		},
	}

	if err := a.healthcheck(); err == nil || !strings.Contains(err.Error(), "status probe failed") {
		t.Fatalf("healthcheck error = %v, want probe failure", err)
	}
}

func TestMonitor_ReturnsOnHealthCheckFailure(t *testing.T) {
	runner := &fakeRunner{
		statusResults: []fakeCmdResult{
			{output: "status failure", err: errors.New("exit status 1")},
		},
	}
	var stdout bytes.Buffer
	a := app{
		runner:          runner,
		stdout:          &stdout,
		stderr:          io.Discard,
		sleep:           func(time.Duration) {},
		getenv:          os.Getenv,
		monitorInterval: time.Millisecond,
	}

	err := a.monitor(context.Background(), asyncProcess{done: make(chan error)})
	if err == nil || !strings.Contains(err.Error(), "status health check failed") {
		t.Fatalf("monitor error = %v, want health-check failure", err)
	}
	if !strings.Contains(stdout.String(), "status failure") {
		t.Fatalf("expected monitor output to include failing status, got %q", stdout.String())
	}
}

func TestMonitor_IgnoresRunJottadLauncherExit(t *testing.T) {
	runner := &fakeRunner{
		statusResults: []fakeCmdResult{
			{output: "ok", err: nil},
			{output: "ok", err: nil},
		},
	}
	a := app{
		runner:          runner,
		stdout:          io.Discard,
		stderr:          io.Discard,
		sleep:           func(time.Duration) {},
		getenv:          envMap(),
		monitorInterval: time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error)
	close(done)

	errCh := make(chan error, 1)
	go func() {
		errCh <- a.monitor(ctx, asyncProcess{done: done})
	}()

	// Monitor's select may pick either the closed tail-done channel (which
	// sets tailDone=nil and loops) or ctx.Done() first; both paths return
	// nil. The timeout below is a safety net, not a synchronization point.
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("monitor error = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("monitor did not return after context cancellation")
	}
}

type fakeCmdResult struct {
	output string
	err    error
}

type fakeRunner struct {
	runResults    map[string][]fakeCmdResult
	statusResults []fakeCmdResult
	ptyErrors     map[string]error
	calls         []string
}

func (r *fakeRunner) Run(name string, args ...string) (string, error) {
	key := cmdKey(name, args)
	r.calls = append(r.calls, "run "+key)
	if len(r.runResults[key]) == 0 {
		return "", nil
	}
	result := r.runResults[key][0]
	r.runResults[key] = r.runResults[key][1:]
	return result.output, result.err
}

func (r *fakeRunner) Start(name string, args []string, stdout, stderr io.Writer) (process, error) {
	r.calls = append(r.calls, "start "+cmdKey(name, args))
	return &fakeProcess{}, nil
}

func (r *fakeRunner) PtyRun(ctx context.Context, name string, args []string, prompts []prompt, timeout time.Duration) error {
	key := cmdKey(name, args)
	r.calls = append(r.calls, "pty "+key)
	if err, ok := r.ptyErrors[key]; ok {
		return err
	}
	return nil
}

func (r *fakeRunner) Status(timeout time.Duration) (string, error) {
	r.calls = append(r.calls, "status")
	if len(r.statusResults) == 0 {
		return "", nil
	}
	result := r.statusResults[0]
	r.statusResults = r.statusResults[1:]
	return result.output, result.err
}

func (r *fakeRunner) called(want string) bool {
	for _, call := range r.calls {
		if call == want {
			return true
		}
	}
	return false
}

type fakeProcess struct {
	waitErr error
}

func (p *fakeProcess) Wait() error {
	return p.waitErr
}

func (p *fakeProcess) Signal(os.Signal) error {
	return nil
}

func (p *fakeProcess) Kill() error {
	return nil
}

func cmdKey(name string, args []string) string {
	if len(args) == 0 {
		return name
	}
	return name + " " + strings.Join(args, " ")
}

func envMap(pairs ...string) func(string) string {
	values := map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		values[pairs[i]] = pairs[i+1]
	}
	return func(key string) string {
		return values[key]
	}
}
