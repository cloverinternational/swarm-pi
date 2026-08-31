// Package main is a Go replacement for native-host.js.
// Chrome native messaging protocol: 4-byte little-endian length prefix + JSON body.
// Bridges between Chrome (stdin/stdout) and chrome-mcp (TCP :18765, newline-delimited JSON).
// Being a compiled binary it works inside snap Chromium's restricted sandbox.
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
	"time"
)

const (
	defaultPort       = 18765
	maxReconnectTries = 60
	reconnectInterval = 500 * time.Millisecond
)

func getPort() int {
	cfgPath := filepath.Join(os.Getenv("HOME"), ".config", "open-claude-in-chrome", "config.json")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return defaultPort
	}
	var cfg struct {
		Port int `json:"port"`
	}
	if json.Unmarshal(data, &cfg) == nil && cfg.Port > 0 {
		return cfg.Port
	}
	return defaultPort
}

// readNative reads one Chrome native messaging message from r.
// Format: 4-byte LE uint32 length, then that many bytes of JSON.
func readNative(r io.Reader) ([]byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	size := binary.LittleEndian.Uint32(lenBuf[:])
	if size == 0 || size > 1024*1024 {
		return nil, fmt.Errorf("invalid message size: %d", size)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

// writeNative writes one Chrome native messaging message to w.
func writeNative(w io.Writer, data []byte) error {
	var lenBuf [4]byte
	binary.LittleEndian.PutUint32(lenBuf[:], uint32(len(data)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

func connectTCP(port int) (net.Conn, error) {
	return net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
}

func main() {
	port := getPort()

	// Connect to chrome-mcp TCP server with retries.
	var tcpConn net.Conn
	for i := 0; i < maxReconnectTries; i++ {
		c, err := connectTCP(port)
		if err == nil {
			tcpConn = c
			break
		}
		time.Sleep(reconnectInterval)
	}
	if tcpConn == nil {
		fmt.Fprintf(os.Stderr, "chrome-native-host: could not connect to chrome-mcp on :%d\n", port)
		os.Exit(1)
	}
	defer tcpConn.Close()

	stdin := bufio.NewReader(os.Stdin)
	stdout := os.Stdout
	tcpReader := bufio.NewReader(tcpConn)

	// stdin → TCP: read native messages, write newline-delimited JSON to TCP.
	go func() {
		for {
			msg, err := readNative(stdin)
			if err != nil {
				// Extension disconnected.
				tcpConn.Close()
				return
			}
			// Validate JSON.
			var raw json.RawMessage
			if json.Unmarshal(msg, &raw) != nil {
				continue
			}
			tcpConn.Write(append(msg, '\n'))
		}
	}()

	// TCP → stdout: read newline-delimited JSON from TCP, write native messages to stdout.
	for {
		line, err := tcpReader.ReadBytes('\n')
		if err != nil {
			// chrome-mcp disconnected — exit so Chrome retries.
			os.Exit(0)
		}
		// Strip the newline, validate JSON, write to Chrome.
		line = line[:len(line)-1]
		if len(line) == 0 {
			continue
		}
		var raw json.RawMessage
		if json.Unmarshal(line, &raw) != nil {
			continue
		}
		if err := writeNative(stdout, line); err != nil {
			os.Exit(0)
		}
	}
}
