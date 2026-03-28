package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/creack/pty"
)

func main() {
	loadEnvFile("/data/jottad/jottad.env")

	if token, err := os.ReadFile("/run/secrets/jotta_token"); err == nil {
		os.Setenv("JOTTA_TOKEN", strings.TrimSpace(string(token)))
	}

	if localtime := os.Getenv("LOCALTIME"); localtime != "" {
		zonePath := filepath.Join("/usr/share/zoneinfo", localtime)
		if !strings.HasPrefix(zonePath, "/usr/share/zoneinfo/") {
			fmt.Fprintf(os.Stderr, "invalid LOCALTIME: %s\n", localtime)
		} else if _, err := os.Stat(zonePath); err == nil {
			os.Remove("/etc/localtime")
			os.Symlink(zonePath, "/etc/localtime")
		}
	}

	if len(os.Args) == 2 && os.Args[1] == "bash" {
		bash := exec.Command("bash")
		bash.Stdin, bash.Stdout, bash.Stderr = os.Stdin, os.Stdout, os.Stderr
		bash.Run()
		return
	}

	must(os.MkdirAll("/data/jottad", 0755))
	forceSymlink("/data/jottad", "/root/.jottad")
	must(os.MkdirAll("/data/jotta-cli", 0755))
	must(os.MkdirAll("/root/.config", 0755))
	forceSymlink("/data/jotta-cli", "/root/.config/jotta-cli")

	jottad := exec.Command("/usr/bin/run_jottad")
	jottad.Stdout, jottad.Stderr = os.Stdout, os.Stderr
	must(jottad.Start())

	time.Sleep(time.Second)

	startupTimeout := envInt("STARTUP_TIMEOUT", 15)
	fmt.Printf("Waiting for jottad to start (timeout: %ds). ", startupTimeout)

	for {
		out, err := jottaStatus(time.Second)
		if err == nil {
			fmt.Println("Jottad started.")
			break
		}

		fmt.Println("Could not start jottad. Checking why.")

		switch {
		case strings.Contains(out, "Found remote device that matches this machine"):
			fmt.Println("Found matching device name, re-using.")
			ptyRun("jotta-cli", []string{"status"}, []prompt{
				{"Do you want to re-use this device? (yes/no): ", "yes"},
			}, time.Second)

		case strings.Contains(out, "Error: The session has been revoked."):
			fmt.Println("Session expired. Logging out and back in.")
			ptyRun("jotta-cli", []string{"logout"}, []prompt{
				{"Backup will stop. Continue?(y/n): ", "y"},
			}, 20*time.Second)
			loginWithToken()

		case strings.Contains(out, "The device name has not been set"):
			fmt.Println("Device name not set, configuring.")
			ptyRun("jotta-cli", []string{"status"}, []prompt{
				{"Device name", os.Getenv("JOTTA_DEVICE")},
			}, 10*time.Second)

		case strings.Contains(out, "Not logged in"):
			fmt.Println("First time login.")
			if err := loginWithToken(); err != nil {
				fmt.Fprintln(os.Stderr, "Login failed:", err)
				os.Exit(1)
			}
		}

		startupTimeout--
		if startupTimeout <= 0 {
			fmt.Println("\nStartup timeout reached.")
			fmt.Println("ERROR: Unable to determine why jottad cannot start:")
			run("jotta-cli", "status")
			os.Exit(1)
		}
		fmt.Printf(".%d.", startupTimeout)
		time.Sleep(time.Second)
	}

	fmt.Println("Adding backup directories.")
	matches, _ := filepath.Glob("/backup/*")
	for _, dir := range matches {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			run("jotta-cli", "add", dir)
		}
	}

	if fi, err := os.Stat("/sync"); err == nil && fi.IsDir() {
		if entries, _ := os.ReadDir("/sync"); len(entries) > 0 {
			fmt.Println("Adding sync directory.")
			run("jotta-cli", "sync", "setup", "--root", "/sync")
		}
	}

	if _, err := os.Stat("/config/ignorefile"); err == nil {
		fmt.Println("Loading ignore file.")
		run("jotta-cli", "ignores", "set", "/config/ignorefile")
	}

	scanInterval := os.Getenv("JOTTA_SCANINTERVAL")
	if scanInterval != "" {
		fmt.Printf("Setting scan interval to %s.\n", scanInterval)
		run("jotta-cli", "config", "set", "scaninterval", scanInterval)
	}

	tail := exec.Command("jotta-cli", "tail")
	tail.Stdout, tail.Stderr = os.Stdout, os.Stderr
	tail.Start()

	for {
		time.Sleep(15 * time.Second)
		if err := exec.Command("jotta-cli", "status").Run(); err != nil {
			fmt.Println("Jottad exited unexpectedly:")
			run("jotta-cli", "status")
			os.Exit(1)
		}
	}
}

type prompt struct {
	match    string
	response string
}

func loginWithToken() error {
	return ptyRun("jotta-cli", []string{"login"}, []prompt{
		{"accept license (yes/no): ", "yes"},
		{"Personal login token: ", os.Getenv("JOTTA_TOKEN")},
		// Only one of the following two prompts will appear:
		{"Device name", os.Getenv("JOTTA_DEVICE")},
		{"Do you want to re-use this device? (yes/no):", "yes"},
	}, 20*time.Second)
}

// jottaStatus runs jotta-cli status via a PTY so it flushes interactive prompts,
// then kills the process after the timeout. Returns combined output and the exit error.
func jottaStatus(timeout time.Duration) (string, error) {
	cmd := exec.Command("jotta-cli", "status")
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return "", err
	}
	defer ptmx.Close()

	var out strings.Builder
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				out.Write(buf[:n])
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
		cmd.Process.Kill()
		ptmx.Close()
		<-readDone
		cmd.Wait()
		return out.String(), fmt.Errorf("timeout")
	}
}

// ptyRun spawns a command with a PTY and responds to prompts as they appear.
// Prompts are matched in list order; only the first unresponded matching prompt
// is answered per read, which correctly handles mutually exclusive alternatives
// (e.g. "Device name" vs "Do you want to re-use this device?").
func ptyRun(name string, args []string, prompts []prompt, timeout time.Duration) error {
	cmd := exec.Command(name, args...)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("pty start %s: %w", name, err)
	}
	defer ptmx.Close()

	deadline := time.Now().Add(timeout)
	buf := make([]byte, 4096)
	accumulated := ""
	responded := make([]bool, len(prompts))

	for time.Now().Before(deadline) {
		ptmx.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, err := ptmx.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			fmt.Print(chunk)
			accumulated += chunk

			for i, p := range prompts {
				if !responded[i] && strings.Contains(accumulated, p.match) {
					ptmx.Write([]byte(p.response + "\n"))
					responded[i] = true
					accumulated = ""
					break
				}
			}
		}
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				continue
			}
			break // EOF or EIO — process exited
		}
	}

	if time.Now().After(deadline) {
		cmd.Process.Kill()
	}
	return cmd.Wait()
}

func run(name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: %s %v: %v\n", name, args, err)
	}
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
		os.Setenv(key, val)
	}
}

func forceSymlink(target, link string) {
	os.Remove(link)
	must(os.Symlink(target, link))
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
