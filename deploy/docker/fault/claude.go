package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	var sid string
	for i, arg := range os.Args[1:] {
		if (arg == "--session-id" || arg == "--resume") && i+2 < len(os.Args) {
			sid = os.Args[i+2]
		}
	}
	if sid == "" {
		fmt.Fprintln(os.Stderr, "stub claude: missing conversation id")
		os.Exit(2)
	}
	home := os.Getenv("HOME")
	logDir := filepath.Join(home, "fault")
	if err := os.MkdirAll(filepath.Join(home, ".claude", "sessions"), 0700); err != nil {
		panic(err)
	}
	if err := os.MkdirAll(logDir, 0700); err != nil {
		panic(err)
	}
	appendLine(filepath.Join(logDir, "launches"), strings.Join(os.Args[1:], " "))
	record, _ := json.Marshal(map[string]any{"sessionId": sid, "pid": os.Getpid()})
	if err := os.WriteFile(filepath.Join(home, ".claude", "sessions", sid+".json"), record, 0600); err != nil {
		panic(err)
	}
	// A muted stub clears the pane instead of drawing its input box, because
	// the previous launch's box would otherwise still be on screen.
	if _, err := os.Stat(filepath.Join(logDir, "mute")); err != nil {
		fmt.Println("›")
	} else {
		fmt.Print("\x1b[2J\x1b[H")
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		prompt := scanner.Text()
		if prompt == "" {
			fmt.Println("›")
			continue
		}
		appendLine(filepath.Join(logDir, "prompts"), prompt)
		if err := os.WriteFile("uncommitted-change", []byte(prompt+"\n"), 0600); err != nil {
			panic(err)
		}
		mode, err := os.ReadFile(filepath.Join(logDir, "mode"))
		if err == nil && strings.TrimSpace(string(mode)) == "hang" {
			for {
				time.Sleep(time.Hour)
			}
		}
		fmt.Println("›")
	}
	if err := scanner.Err(); err != nil {
		panic(err)
	}
}

func appendLine(path, line string) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if _, err := fmt.Fprintln(f, line); err != nil {
		panic(err)
	}
}
