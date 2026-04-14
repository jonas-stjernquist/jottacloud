// fake-cli simulates jotta-cli's interactive prompt behavior for testing.
// It reads a JSON scenario from the FAKECLI_SCENARIO environment variable.
//
// Each step prints a prompt string (without trailing newline, matching jotta-cli)
// and reads a response line from stdin. If "expect" is set, it validates the response.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

type step struct {
	Prompt       string `json:"prompt"`
	PromptSuffix string `json:"promptSuffix,omitempty"`
	// PromptSuffixDelayMs emits PromptSuffix in a later write so tests can model
	// prompt text and terminal queries arriving in separate PTY reads.
	PromptSuffixDelayMs int    `json:"promptSuffixDelayMs,omitempty"`
	Expect              string `json:"expect,omitempty"`
	// ExpectQueryReplies consumes terminal-query responses from stdin before
	// reading the interactive answer. This lets tests model CLIs that negotiate
	// terminal capabilities before reading user input.
	ExpectQueryReplies []string `json:"expectQueryReplies,omitempty"`
	// QuietMs fails the step if extra input arrives before the delay elapses.
	// This is used to ensure prompt answers are not piggybacked on terminal-query
	// replies in the same PTY burst.
	QuietMs int `json:"quietMs,omitempty"`
	// DelayMs adds a delay before printing the prompt (simulates slow output).
	DelayMs int `json:"delayMs,omitempty"`
	// ChunkSize splits the prompt into chunks of this size (simulates partial reads).
	ChunkSize int `json:"chunkSize,omitempty"`
}

type scenario struct {
	Steps       []step `json:"steps"`
	FinalOutput string `json:"finalOutput,omitempty"`
	ExitCode    int    `json:"exitCode"`
	// HangForever causes the process to sleep indefinitely after steps (for timeout tests).
	HangForever bool `json:"hangForever,omitempty"`
	// RawMode disables ICRNL/ICANON on stdin so \r is delivered as-is (not converted
	// to \n), matching interactive CLIs that put stdin in raw mode. When true, all
	// steps read until \r instead of \n.
	RawMode bool `json:"rawMode,omitempty"`
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

	// If the scenario uses raw mode, put stdin into raw mode before printing
	// the first prompt so that \r from the PTY master is delivered as-is rather
	// than being converted to \n. term.MakeRaw is used for portability across
	// platforms (Linux, macOS, etc.).
	if sc.RawMode {
		oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			fmt.Fprintf(os.Stderr, "term.MakeRaw: %v\n", err)
			os.Exit(2)
		}
		defer term.Restore(int(os.Stdin.Fd()), oldState)
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

		if s.PromptSuffixDelayMs > 0 {
			time.Sleep(time.Duration(s.PromptSuffixDelayMs) * time.Millisecond)
		}

		if s.PromptSuffix != "" {
			if s.ChunkSize > 0 {
				for i := 0; i < len(s.PromptSuffix); i += s.ChunkSize {
					end := i + s.ChunkSize
					if end > len(s.PromptSuffix) {
						end = len(s.PromptSuffix)
					}
					fmt.Print(s.PromptSuffix[i:end])
					time.Sleep(10 * time.Millisecond)
				}
			} else {
				fmt.Print(s.PromptSuffix)
			}
		}

		for _, want := range s.ExpectQueryReplies {
			buf := make([]byte, len(want))
			if _, err := io.ReadFull(os.Stdin, buf); err != nil {
				fmt.Fprintf(os.Stderr, "query read error: %v\n", err)
				os.Exit(2)
			}
			if got := string(buf); got != want {
				fmt.Fprintf(os.Stderr, "expected query reply %q, got %q\n", want, got)
				os.Exit(1)
			}
		}

		if s.QuietMs > 0 {
			if hasImmediateInput(os.Stdin, time.Duration(s.QuietMs)*time.Millisecond) {
				fmt.Fprintln(os.Stderr, "unexpected immediate input after terminal query reply")
				os.Exit(1)
			}
			if err := unix.SetNonblock(int(os.Stdin.Fd()), false); err != nil {
				fmt.Fprintf(os.Stderr, "reset nonblock: %v\n", err)
				os.Exit(2)
			}
		}

		delim := byte('\n')
		if sc.RawMode {
			// ICRNL is disabled, so \r arrives as-is from the master.
			delim = '\r'
		}
		line, err := readUntil(os.Stdin, delim)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read error: %v\n", err)
			os.Exit(2)
		}
		line = strings.TrimRight(line, "\r\n")

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

func readUntil(f *os.File, delim byte) (string, error) {
	var out []byte
	buf := make([]byte, 1)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
			if buf[0] == delim {
				return string(out), nil
			}
		}
		if err != nil {
			return "", err
		}
	}
}

func hasImmediateInput(f *os.File, quiet time.Duration) bool {
	fd := int(f.Fd())
	if err := unix.SetNonblock(fd, true); err != nil {
		fmt.Fprintf(os.Stderr, "set nonblock: %v\n", err)
		os.Exit(2)
	}
	buf := make([]byte, 1)
	deadline := time.Now().Add(quiet)
	for time.Now().Before(deadline) {
		n, err := f.Read(buf)
		if n > 0 {
			return true
		}
		if err != nil && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EWOULDBLOCK) {
			fmt.Fprintf(os.Stderr, "quiet check read error: %v\n", err)
			os.Exit(2)
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}
