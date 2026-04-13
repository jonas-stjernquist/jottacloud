// fake-cli simulates jotta-cli's interactive prompt behavior for testing.
// It reads a JSON scenario from the FAKECLI_SCENARIO environment variable.
//
// Each step prints a prompt string (without trailing newline, matching jotta-cli)
// and reads a response line from stdin. If "expect" is set, it validates the response.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

type step struct {
	Prompt string `json:"prompt"`
	Expect string `json:"expect,omitempty"`
	// DelayMs adds a delay before printing the prompt (simulates slow output).
	DelayMs int `json:"delayMs,omitempty"`
	// ChunkSize splits the prompt into chunks of this size (simulates partial reads).
	ChunkSize int `json:"chunkSize,omitempty"`
	// RawMode reads until \r (carriage return) instead of \n, simulating interactive
	// CLIs that put stdin in raw mode and treat \r as the Enter key.
	RawMode bool `json:"rawMode,omitempty"`
}

type scenario struct {
	Steps       []step `json:"steps"`
	FinalOutput string `json:"finalOutput,omitempty"`
	ExitCode    int    `json:"exitCode"`
	// HangForever causes the process to sleep indefinitely after steps (for timeout tests).
	HangForever bool `json:"hangForever,omitempty"`
}

func main() {
	data := os.Getenv("FAKECLI_SCENARIO")
	if data == "" {
		fmt.Fprintln(os.Stderr, "FAKECLI_SCENARIO not set")
		os.Exit(2)
	}

	var sc scenario
	if err := json.Unmarshal([]byte(data), &sc); err != nil {
		fmt.Fprintf(os.Stderr, "bad scenario JSON: %v\n", err)
		os.Exit(2)
	}

	reader := bufio.NewReader(os.Stdin)

	// If any step uses raw mode (reads until \r), disable ICRNL now — before
	// printing the first prompt — so that \r from the PTY master is delivered
	// as-is instead of being converted to \n. This must happen before ptyRun
	// can write a response, otherwise the conversion may already have occurred.
	for _, s := range sc.Steps {
		if s.RawMode {
			disableICRNL(os.Stdin)
			break
		}
	}

	for _, s := range sc.Steps {
		if s.DelayMs > 0 {
			time.Sleep(time.Duration(s.DelayMs) * time.Millisecond)
		}

		if s.ChunkSize > 0 {
			// Write prompt in chunks to simulate partial PTY reads.
			for i := 0; i < len(s.Prompt); i += s.ChunkSize {
				end := i + s.ChunkSize
				if end > len(s.Prompt) {
					end = len(s.Prompt)
				}
				fmt.Print(s.Prompt[i:end])
				time.Sleep(10 * time.Millisecond)
			}
		} else {
			fmt.Print(s.Prompt)
		}

		var (
			line string
			err  error
		)
		if s.RawMode {
			// ICRNL was disabled at startup, so \r arrives as-is from the master.
			line, err = reader.ReadString('\r')
			if err != nil {
				fmt.Fprintf(os.Stderr, "read error: %v\n", err)
				os.Exit(2)
			}
			line = strings.TrimRight(line, "\r")
		} else {
			line, err = reader.ReadString('\n')
			if err != nil {
				fmt.Fprintf(os.Stderr, "read error: %v\n", err)
				os.Exit(2)
			}
			line = strings.TrimRight(line, "\r\n")
		}

		if s.Expect != "" && line != s.Expect {
			fmt.Fprintf(os.Stderr, "expected %q, got %q\n", s.Expect, line)
			os.Exit(1)
		}
	}

	if sc.FinalOutput != "" {
		fmt.Print(sc.FinalOutput)
	}

	if sc.HangForever {
		select {}
	}

	os.Exit(sc.ExitCode)
}

// disableICRNL clears the ICRNL and ICANON flags on f's file descriptor.
// ICRNL: stops the PTY from converting incoming \r to \n, so \r arrives as-is.
// ICANON: disables line buffering so characters are delivered immediately rather
// than waiting for a newline. Together these mirror what interactive CLIs do
// when they put their stdin into raw mode to handle \r as the Enter key.
func disableICRNL(f *os.File) {
	var t syscall.Termios
	syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TCGETS), uintptr(unsafe.Pointer(&t)))
	t.Iflag &^= syscall.ICRNL  // don't convert \r→\n on input
	t.Lflag &^= syscall.ICANON // deliver characters immediately, don't buffer lines
	t.Cc[syscall.VMIN] = 1     // block until at least 1 byte is available
	t.Cc[syscall.VTIME] = 0    // no read timeout
	syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TCSETS), uintptr(unsafe.Pointer(&t)))
}
