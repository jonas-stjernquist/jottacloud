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
	"time"

	"golang.org/x/term"
)

type step struct {
	Prompt string `json:"prompt"`
	Expect string `json:"expect,omitempty"`
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

	reader := bufio.NewReader(os.Stdin)

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

		var (
			line string
			err  error
		)
		if sc.RawMode {
			// ICRNL is disabled, so \r arrives as-is from the master.
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
