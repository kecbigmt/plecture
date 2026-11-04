package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
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
	if _, err := os.Stat(filepath.Join(logDir, "mute")); os.IsNotExist(err) {
		runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
		if runtimeDir == "" {
			runtimeDir = "/tmp"
		}
		socket := filepath.Join(runtimeDir, "claude-channel", sid+".sock")
		if err := os.MkdirAll(filepath.Dir(socket), 0700); err != nil {
			panic(err)
		}
		if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
			panic(err)
		}
		listener, err := net.Listen("unix", socket)
		if err != nil {
			panic(err)
		}
		defer listener.Close()
		go recordEvents(listener, filepath.Join(logDir, "prompts"))
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "sessions", sid+".json"), record, 0600); err != nil {
		panic(err)
	}
	fmt.Println("›")
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

func recordEvents(listener net.Listener, prompts string) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			header := make([]byte, 4)
			if _, err := io.ReadFull(conn, header); err != nil {
				return
			}
			data := make([]byte, binary.BigEndian.Uint32(header))
			if _, err := io.ReadFull(conn, data); err != nil {
				return
			}
			var envelope struct {
				Payload struct {
					Text string `json:"text"`
				} `json:"payload"`
			}
			if err := json.Unmarshal(data, &envelope); err != nil {
				return
			}
			var message struct {
				Body string `json:"body"`
			}
			if err := json.Unmarshal([]byte(envelope.Payload.Text), &message); err != nil {
				return
			}
			appendLine(prompts, message.Body)
		}()
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
