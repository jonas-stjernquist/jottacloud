package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/creack/pty"
)

const (
	dataDir                = "/data/jottad"
	configDir              = "/data/jotta-cli"
	ignoreFilePath         = "/config/ignorefile"
	secretTokenPath        = "/run/secrets/jotta_token"
	localtimeRoot          = "/usr/share/zoneinfo"
	startupProbeTimeout    = time.Second
	syncStatusTimeout      = 5 * time.Second
	loginTimeout           = 20 * time.Second
	logoutTimeout          = 20 * time.Second
	devicePromptTimeout    = 10 * time.Second
	syncSetupTimeout       = 30 * time.Second
	defaultMonitorInterval = 15 * time.Second
	setupSettlingDelay     = 3 * time.Second
	shutdownGracePeriod    = 5 * time.Second
	terminalSettleDelay    = 50 * time.Millisecond
	readPollInterval       = 10 * time.Millisecond
	promptLicense          = "accept license (yes/no): "
	promptToken            = "Personal login token: "
	promptDeviceName       = "Device name"
	promptReuseDevice      = "Do you want to re-use this device? (yes/no):"
	promptLogout           = "Backup will stop. Continue?(y/n): "
	promptSyncContinue     = "Continue sync setup? [yes]:"
	promptSyncErrors       = "Chose the error reporting mode for sync:"
	promptSelectiveSync    = "Do you want to setup selective sync? (y/n):"
	statusMatchingDevice   = "Found remote device that matches this machine"
	statusSessionRevoked   = "Error: The session has been revoked."
	statusNoDeviceName     = "The device name has not been set"
	statusNotLoggedIn      = "Not logged in"
	statusDeviceMissing    = "does not exist remotely"
	statusSyncDisabled     = "Sync is not enabled"
	queryDSR               = "\x1b[6n"
	queryOSC11             = "\x1b]11;?\x1b\\"
	replyDSR               = "\x1b[1;1R"
	replyOSC11             = "\x1b]11;rgb:0000/0000/0000\x1b\\"
)

var (
	ptyOutput io.Writer = os.Stdout
	// jottaCLI is overridable in tests; do not mutate in production code.
	jottaCLI = "jotta-cli"

	errPtyTimeout    = errors.New("pty timeout")
	errStatusTimeout = errors.New("status timeout")
)

type prompt struct {
	match    string
	response string
}

type statusKind string

const (
	statusUnknown            statusKind = "unknown"
	statusMatchingDeviceKind statusKind = "matching_device"
	statusSessionRevokedKind statusKind = "session_revoked"
	statusNoDeviceNameKind   statusKind = "no_device_name"
	statusNotLoggedInKind    statusKind = "not_logged_in"
	statusDeviceMissingKind  statusKind = "device_missing"
)

type process interface {
	Wait() error
	Signal(os.Signal) error
	Kill() error
}

type commandRunner interface {
	Run(name string, args ...string) (string, error)
	Start(name string, args []string, stdout, stderr io.Writer) (process, error)
	PtyRun(name string, args []string, prompts []prompt, timeout time.Duration) error
	Status(timeout time.Duration) (string, error)
}

type execRunner struct{}

type execProcess struct {
	cmd *exec.Cmd
}

type app struct {
	runner          commandRunner
	stdout          io.Writer
	stderr          io.Writer
	sleep           func(time.Duration)
	getenv          func(string) string
	setenv          func(string, string) error
	monitorInterval time.Duration
}

type terminalResponder struct {
	pending string
	queries []terminalQuery
}

type asyncProcess struct {
	proc process
	done chan error
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a := app{
		runner:          execRunner{},
		stdout:          os.Stdout,
		stderr:          os.Stderr,
		sleep:           time.Sleep,
		getenv:          os.Getenv,
		setenv:          os.Setenv,
		monitorInterval: defaultMonitorInterval,
	}

	if err := a.run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func (a app) run(ctx context.Context, args []string) error {
	loadEnvFile(filepath.Join(dataDir, "jottad.env"))
	a.configureMonitor()

	if token, err := os.ReadFile(secretTokenPath); err == nil {
		if trimmed := strings.TrimSpace(string(token)); trimmed != "" && a.setenv != nil {
			_ = a.setenv("JOTTA_TOKEN", trimmed)
		}
	}

	if err := configureLocaltime(a.getenv("LOCALTIME")); err != nil {
		fmt.Fprintf(a.stderr, "warning: %v\n", err)
	}

	if len(args) == 1 && args[0] == "bash" {
		if a.getenv("JOTTA_DEV") != "1" {
			return errors.New("bash subcommand requires JOTTA_DEV=1")
		}
		return runBash()
	}

	if err := preparePersistentPaths(); err != nil {
		return err
	}

	jottad, err := startAsyncProcess(a.runner, "/usr/bin/run_jottad", nil, a.stdout, a.stderr)
	if err != nil {
		return fmt.Errorf("start jottad: %w", err)
	}
	defer terminateProcess(jottad, shutdownGracePeriod)

	a.sleep(time.Second)

	if err := a.waitForStartup(ctx); err != nil {
		return err
	}
	// Graceful shutdown: if the context was cancelled during startup (e.g. SIGTERM),
	// skip configuration steps and let the deferred terminateProcess drain jottad.
	if ctx.Err() != nil {
		return nil
	}
	if err := a.configureBackups(); err != nil {
		return err
	}
	if err := a.configureSync(); err != nil {
		return err
	}
	if err := a.loadIgnoreFile(ignoreFilePath); err != nil {
		return err
	}
	if err := a.configureScanInterval(); err != nil {
		return err
	}

	tail, err := startAsyncProcess(a.runner, jottaCLI, []string{"tail"}, a.stdout, a.stderr)
	if err != nil {
		return fmt.Errorf("start tail: %w", err)
	}
	defer terminateProcess(tail, shutdownGracePeriod)

	fmt.Fprintln(a.stdout, "Monitoring active.")
	return a.monitor(ctx, tail)
}

func (a app) waitForStartup(ctx context.Context) error {
	startupTimeout := envInt("STARTUP_TIMEOUT", 15)
	fmt.Fprintf(a.stdout, "Waiting for jottad to start (timeout: %ds). ", startupTimeout)

	for remaining := startupTimeout; remaining > 0; remaining-- {
		if err := ctx.Err(); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}

		out, err := a.runner.Status(startupProbeTimeout)
		if err == nil {
			fmt.Fprintln(a.stdout, "Jottad started.")
			return nil
		}

		fmt.Fprintln(a.stdout, "Could not start jottad. Checking why.")
		if err := a.handleStartupStatus(classifyStatus(out)); err != nil {
			return err
		}

		if remaining == 1 {
			break
		}
		fmt.Fprintf(a.stdout, ".%d.", remaining-1)
		a.sleep(time.Second)
	}

	fmt.Fprintln(a.stdout, "\nStartup timeout reached.")
	fmt.Fprintln(a.stdout, "ERROR: Unable to determine why jottad cannot start:")
	if out, err := a.runner.Run(jottaCLI, "status"); out != "" {
		fmt.Fprint(a.stdout, out)
		if err != nil && !strings.HasSuffix(out, "\n") {
			fmt.Fprintln(a.stdout)
		}
	}
	return errors.New("startup timeout")
}

func (a app) handleStartupStatus(kind statusKind) error {
	switch kind {
	case statusMatchingDeviceKind:
		fmt.Fprintln(a.stdout, "Found matching device name, re-using.")
		return a.runner.PtyRun(jottaCLI, []string{"status"}, []prompt{
			{promptReuseDevice, "yes"},
		}, startupProbeTimeout)
	case statusSessionRevokedKind:
		fmt.Fprintln(a.stdout, "Session expired. Logging out and back in.")
		if err := a.logout(); err != nil {
			return err
		}
		return a.loginWithToken()
	case statusNoDeviceNameKind:
		device := a.getenv("JOTTA_DEVICE")
		if device == "" {
			return errors.New("JOTTA_DEVICE is not set")
		}
		fmt.Fprintln(a.stdout, "Device name not set, configuring.")
		return a.runner.PtyRun(jottaCLI, []string{"status"}, []prompt{
			{promptDeviceName, device},
		}, devicePromptTimeout)
	case statusNotLoggedInKind:
		fmt.Fprintln(a.stdout, "First time login.")
		return a.loginWithToken()
	case statusDeviceMissingKind:
		fmt.Fprintln(a.stdout, "Device not found remotely. Logging out and back in.")
		if err := a.logout(); err != nil {
			return err
		}
		return a.loginWithToken()
	default:
		return nil
	}
}

func (a app) configureBackups() error {
	return a.configureBackupsIn("/backup/*")
}

func (a app) configureBackupsIn(globPattern string) error {
	fmt.Fprintln(a.stdout, "Adding backup directories.")
	matches, err := filepath.Glob(globPattern)
	if err != nil {
		return fmt.Errorf("scan backup directories: %w", err)
	}

	addedAny := false
	for _, dir := range matches {
		fi, statErr := os.Stat(dir)
		if statErr != nil || !fi.IsDir() {
			continue
		}
		out, err := a.runChecked(jottaCLI, "add", dir)
		if err != nil {
			if strings.Contains(out, "already added to backup") {
				continue
			}
			return err
		}
		addedAny = true
	}

	if addedAny {
		a.sleep(setupSettlingDelay)
	}
	return nil
}

func (a app) configureSync() error {
	fi, err := os.Stat("/sync")
	if err != nil || !fi.IsDir() {
		return nil
	}

	entries, err := os.ReadDir("/sync")
	if err != nil {
		fmt.Fprintf(a.stdout, "Warning: unable to read /sync, skipping sync setup: %v\n", err)
		return nil
	}
	if len(entries) == 0 {
		return nil
	}

	fmt.Fprintln(a.stdout, "Adding sync directory.")
	if err := a.ensureSyncConfigured(); err != nil {
		return err
	}

	_, err = a.runChecked(jottaCLI, "sync", "start")
	return err
}

func (a app) ensureSyncConfigured() error {
	out, err := a.runner.Status(syncStatusTimeout)
	if strings.Contains(out, statusSyncDisabled) {
		if err := a.runner.PtyRun(jottaCLI, []string{"sync", "setup", "--root", "/sync"}, []prompt{
			{promptSyncContinue, "yes"},
			{promptSyncErrors, "off"},
			{promptSelectiveSync, "n"},
		}, syncSetupTimeout); err != nil {
			return fmt.Errorf("setup sync: %w", err)
		}
		return nil
	}
	if err != nil {
		fmt.Fprintf(a.stdout, "Warning: sync status probe failed, continuing with sync start: %v\n", err)
	}
	return nil
}

func (a app) loadIgnoreFile(path string) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}

	fmt.Fprintln(a.stdout, "Loading ignore file.")
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open ignore file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, err := a.runChecked(jottaCLI, "ignores", "add", "--pattern", line); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read ignore file: %w", err)
	}
	return nil
}

func (a app) configureScanInterval() error {
	scanInterval := a.getenv("JOTTA_SCANINTERVAL")
	if scanInterval == "" {
		return nil
	}

	fmt.Fprintf(a.stdout, "Setting scan interval to %s.\n", scanInterval)
	_, err := a.runChecked(jottaCLI, "config", "set", "scaninterval", scanInterval)
	return err
}

func (a app) monitor(ctx context.Context, tail asyncProcess) error {
	ticker := time.NewTicker(a.monitorInterval)
	defer ticker.Stop()

	tailDone := tail.done

	for {
		select {
		case <-ctx.Done():
			return nil
		case err, ok := <-tailDone:
			if !ok {
				tailDone = nil
				continue
			}
			if err != nil {
				return fmt.Errorf("jotta-cli tail exited unexpectedly: %w", err)
			}
			return errors.New("jotta-cli tail exited unexpectedly")
		case <-ticker.C:
			out, err := a.runner.Run(jottaCLI, "status")
			if err != nil {
				fmt.Fprintln(a.stdout, "Jottad exited unexpectedly:")
				if out != "" {
					fmt.Fprint(a.stdout, out)
					if !strings.HasSuffix(out, "\n") {
						fmt.Fprintln(a.stdout)
					}
				}
				return fmt.Errorf("status health check failed: %w", err)
			}
		}
	}
}

func (a *app) configureMonitor() {
	a.monitorInterval = envDurationSecondsFrom(a.getenv, "JOTTA_MONITOR_INTERVAL_SECONDS", a.monitorInterval)
	if a.monitorInterval <= 0 {
		a.monitorInterval = defaultMonitorInterval
	}
}

func (a app) logout() error {
	if err := a.runner.PtyRun(jottaCLI, []string{"logout"}, []prompt{
		{promptLogout, "y"},
	}, logoutTimeout); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	return nil
}

func (a app) loginWithToken() error {
	if err := loginWithTokenWithRunner(a.runner, a.getenv); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}
	return nil
}

func (a app) runChecked(name string, args ...string) (string, error) {
	out, err := a.runner.Run(name, args...)
	if err == nil {
		return out, nil
	}
	return out, formatCommandError(name, args, out, err)
}

func loginWithToken() error {
	return loginWithTokenWithRunner(execRunner{}, os.Getenv)
}

func loginWithTokenWithRunner(runner commandRunner, getenv func(string) string) error {
	token := getenv("JOTTA_TOKEN")
	if token == "" {
		return errors.New("JOTTA_TOKEN is not set")
	}
	device := getenv("JOTTA_DEVICE")
	if device == "" {
		return errors.New("JOTTA_DEVICE is not set")
	}
	return runner.PtyRun(jottaCLI, []string{"login"}, []prompt{
		{promptLicense, "yes"},
		{promptToken, token},
		{promptDeviceName, device},
		{promptReuseDevice, "yes"},
	}, loginTimeout)
}

func ptyRun(name string, args []string, prompts []prompt, timeout time.Duration) error {
	cmd := exec.Command(name, args...)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("pty start %s: %w", name, err)
	}
	defer ptmx.Close()

	buf := make([]byte, 4096)
	accumulated := ""
	responded := make([]bool, len(prompts))
	pendingPrompt := -1
	pendingPromptReadyAt := time.Time{}
	responder := newTerminalResponder(prompts)

	deadlineTimer := time.NewTimer(timeout)
	defer deadlineTimer.Stop()

	type readResult struct {
		chunk string
		err   error
	}
	readCh := make(chan readResult, 16)
	stopCh := make(chan struct{})
	defer close(stopCh)
	go func() {
		defer close(readCh)
		sendResult := func(result readResult) bool {
			select {
			case readCh <- result:
				return true
			case <-stopCh:
				return false
			}
		}

		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				if !sendResult(readResult{chunk: string(buf[:n])}) {
					return
				}
			}
			if readErr != nil {
				if !sendResult(readResult{err: readErr}) {
					return
				}
				return
			}
		}
	}()

	ticker := time.NewTicker(readPollInterval)
	defer ticker.Stop()

	sendPrompt := func(index int) {
		_, _ = ptmx.Write([]byte(prompts[index].response + "\r"))
		responded[index] = true
		accumulated = ""
		pendingPrompt = -1
		pendingPromptReadyAt = time.Time{}
	}

	for {
		select {
		case result, ok := <-readCh:
			if !ok {
				return cmd.Wait()
			}
			if result.chunk != "" {
				fmt.Fprint(ptyOutput, result.chunk)
				hadTerminalQuery := responder.respond(ptmx, result.chunk)
				accumulated += result.chunk

				if pendingPrompt == -1 {
					for i, p := range prompts {
						if !responded[i] && strings.Contains(accumulated, p.match) {
							pendingPrompt = i
							pendingPromptReadyAt = time.Now().Add(terminalSettleDelay)
							break
						}
					}
				}
				if pendingPrompt != -1 && hadTerminalQuery {
					pendingPromptReadyAt = time.Now().Add(terminalSettleDelay)
				}
			}
			if result.err != nil {
				return cmd.Wait()
			}
		case <-ticker.C:
			if pendingPrompt != -1 && time.Now().After(pendingPromptReadyAt) {
				sendPrompt(pendingPrompt)
			}
		case <-deadlineTimer.C:
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return fmt.Errorf("%s: %w", name, errPtyTimeout)
		}
	}
}

func (execRunner) Run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

func (execRunner) Start(name string, args []string, stdout, stderr io.Writer) (process, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return execProcess{cmd: cmd}, nil
}

func (execRunner) PtyRun(name string, args []string, prompts []prompt, timeout time.Duration) error {
	return ptyRun(name, args, prompts, timeout)
}

func (execRunner) Status(timeout time.Duration) (string, error) {
	cmd := exec.Command(jottaCLI, "status")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return "", err
	}
	defer ptmx.Close()

	var out strings.Builder
	responder := terminalResponder{queries: terminalQueries}
	readDone := make(chan struct{})

	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				chunk := string(buf[:n])
				out.WriteString(chunk)
				responder.respond(ptmx, chunk)
			}
			if readErr != nil {
				return
			}
		}
	}()

	select {
	case <-readDone:
		return out.String(), cmd.Wait()
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		_ = ptmx.Close()
		<-readDone
		_ = cmd.Wait()
		return out.String(), errStatusTimeout
	}
}

func (p execProcess) Wait() error {
	return p.cmd.Wait()
}

func (p execProcess) Signal(sig os.Signal) error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Signal(sig)
}

func (p execProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

func (r *terminalResponder) respond(ptmx *os.File, chunk string) bool {
	r.pending += chunk
	answered := false
	queries := r.queries
	if len(queries) == 0 {
		queries = terminalQueries
	}

	for {
		matched := false
		for _, query := range queries {
			if idx := strings.Index(r.pending, query.seq); idx >= 0 {
				_, _ = ptmx.Write([]byte(query.reply))
				r.pending = r.pending[:idx] + r.pending[idx+len(query.seq):]
				answered = true
				matched = true
				break
			}
		}
		if !matched {
			break
		}
	}

	r.pending = terminalQuerySuffix(r.pending, queries)
	return answered
}

type terminalQuery struct {
	seq   string
	reply string
}

var terminalQueries = []terminalQuery{
	{seq: queryDSR, reply: replyDSR},
	{seq: queryOSC11, reply: replyOSC11},
}

var interactiveTerminalQueries = []terminalQuery{
	{seq: queryDSR, reply: replyDSR},
}

func newTerminalResponder(prompts []prompt) terminalResponder {
	if len(prompts) > 0 {
		// Prompted flows misparse OSC 11 replies as user input, so only answer the
		// DSR cursor-position query while interacting with login/logout/setup prompts.
		return terminalResponder{queries: interactiveTerminalQueries}
	}
	return terminalResponder{queries: terminalQueries}
}

func terminalQuerySuffix(s string, queries []terminalQuery) string {
	best := ""
	for _, query := range queries {
		for i := 1; i < len(query.seq); i++ {
			prefix := query.seq[:i]
			if strings.HasSuffix(s, prefix) && len(prefix) > len(best) {
				best = prefix
			}
		}
	}
	return best
}

func classifyStatus(output string) statusKind {
	switch {
	case strings.Contains(output, statusMatchingDevice):
		return statusMatchingDeviceKind
	case strings.Contains(output, statusSessionRevoked):
		return statusSessionRevokedKind
	case strings.Contains(output, statusNoDeviceName):
		return statusNoDeviceNameKind
	case strings.Contains(output, statusNotLoggedIn):
		return statusNotLoggedInKind
	case strings.Contains(output, statusDeviceMissing):
		return statusDeviceMissingKind
	default:
		return statusUnknown
	}
}

func startAsyncProcess(runner commandRunner, name string, args []string, stdout, stderr io.Writer) (asyncProcess, error) {
	proc, err := runner.Start(name, args, stdout, stderr)
	if err != nil {
		return asyncProcess{}, err
	}

	done := make(chan error, 1)
	go func() {
		done <- proc.Wait()
		close(done)
	}()

	return asyncProcess{proc: proc, done: done}, nil
}

func terminateProcess(proc asyncProcess, grace time.Duration) {
	if proc.proc == nil || proc.done == nil {
		return
	}

	select {
	case <-proc.done:
		return
	default:
	}

	_ = proc.proc.Signal(syscall.SIGTERM)
	select {
	case <-proc.done:
		return
	case <-time.After(grace):
		_ = proc.proc.Kill()
		<-proc.done
	}
}

func preparePersistentPaths() error {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("create %s: %w", dataDir, err)
	}
	if err := forceSymlink(dataDir, "/root/.jottad"); err != nil {
		return err
	}

	if err := os.MkdirAll(configDir, 0755); err != nil {
		return fmt.Errorf("create %s: %w", configDir, err)
	}
	if err := os.MkdirAll("/root/.config", 0755); err != nil {
		return fmt.Errorf("create /root/.config: %w", err)
	}
	if err := forceSymlink(configDir, "/root/.config/jotta-cli"); err != nil {
		return err
	}
	return nil
}

func configureLocaltime(localtime string) error {
	if localtime == "" {
		return nil
	}

	zonePath := filepath.Clean(filepath.Join(localtimeRoot, localtime))
	if !strings.HasPrefix(zonePath, localtimeRoot+string(os.PathSeparator)) {
		return fmt.Errorf("invalid LOCALTIME: %s", localtime)
	}
	if _, err := os.Stat(zonePath); err != nil {
		return fmt.Errorf("invalid LOCALTIME: %s", localtime)
	}
	if err := os.Remove("/etc/localtime"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("replace /etc/localtime: %w", err)
	}
	if err := os.Symlink(zonePath, "/etc/localtime"); err != nil {
		return fmt.Errorf("link localtime: %w", err)
	}
	return nil
}

func runBash() error {
	bash := exec.Command("bash")
	bash.Stdin = os.Stdin
	bash.Stdout = os.Stdout
	bash.Stderr = os.Stderr
	return bash.Run()
}

func formatCommandError(name string, args []string, out string, err error) error {
	if trimmed := strings.TrimSpace(out); trimmed != "" {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, trimmed)
	}
	return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
}

func loadEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		key := line[:idx]
		val := strings.Trim(line[idx+1:], `"'`)
		_ = os.Setenv(key, val)
	}
}

func forceSymlink(target, link string) error {
	info, err := os.Lstat(link)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(link); err != nil {
				return fmt.Errorf("remove existing symlink %s: %w", link, err)
			}
		} else if info.IsDir() {
			return fmt.Errorf("refusing to replace non-symlink directory at %s", link)
		} else {
			if err := os.Remove(link); err != nil {
				return fmt.Errorf("remove existing file %s: %w", link, err)
			}
		}
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("stat %s: %w", link, err)
	}
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("symlink %s -> %s: %w", link, target, err)
	}
	return nil
}

func envInt(key string, def int) int {
	return envIntFrom(os.Getenv, key, def)
}

func envIntFrom(getenv func(string) string, key string, def int) int {
	if v := getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDurationSecondsFrom(getenv func(string) string, key string, def time.Duration) time.Duration {
	v := getenv(key)
	if v == "" {
		return def
	}
	seconds, err := strconv.Atoi(v)
	if err != nil || seconds <= 0 {
		return def
	}
	return time.Duration(seconds) * time.Second
}
