package analytics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/paths"
)

func HashMachineID(namespace, raw string) string {
	return HashValue(namespace, raw)
}

func HashValue(namespace, raw string) string {
	hasher := sha256.New()
	hasher.Write([]byte(strings.TrimSpace(namespace)))
	hasher.Write([]byte{0})
	hasher.Write([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(hasher.Sum(nil))
}

func MachineIDHash(namespace string) (string, error) {
	raw, err := rawMachineID()
	if err != nil {
		return "", err
	}
	return HashValue(namespace, raw), nil
}

func DeviceIDHash(namespace string) (string, error) {
	if raw := strings.TrimSpace(os.Getenv("SWARM_ANALYTICS_DEVICE_ID")); raw != "" {
		return HashValue(namespace, raw), nil
	}
	if raw := readCloudDeviceID(); raw != "" {
		return HashValue(namespace, raw), nil
	}
	return MachineIDHash(namespace)
}

func WorkspaceHash(namespace, workspacePath string) string {
	trimmed := strings.TrimSpace(workspacePath)
	if trimmed == "" {
		return ""
	}
	if abs, err := filepath.Abs(trimmed); err == nil {
		trimmed = abs
	}
	if resolved, err := filepath.EvalSymlinks(trimmed); err == nil {
		trimmed = resolved
	}
	return HashValue(namespace, trimmed)
}

func WorkspaceLabel(workspacePath string) string {
	trimmed := strings.TrimSpace(workspacePath)
	if trimmed == "" {
		return ""
	}
	if abs, err := filepath.Abs(trimmed); err == nil {
		trimmed = abs
	}
	label := filepath.Base(trimmed)
	if label == "." || label == string(filepath.Separator) {
		return ""
	}
	return label
}

func readCloudDeviceID() string {
	body, err := os.ReadFile(paths.In("cloud.json"))
	if err != nil {
		return ""
	}
	var cfg struct {
		DeviceID string `json:"device_id"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		return ""
	}
	return strings.TrimSpace(cfg.DeviceID)
}

func rawMachineID() (string, error) {
	switch runtime.GOOS {
	case "linux":
		for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
			if data, err := os.ReadFile(path); err == nil {
				if trimmed := strings.TrimSpace(string(data)); trimmed != "" {
					return trimmed, nil
				}
			}
		}
	case "darwin":
		output, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
		if err == nil {
			for line := range strings.SplitSeq(string(output), "\n") {
				if strings.Contains(line, "IOPlatformUUID") {
					parts := strings.SplitN(line, "=", 2)
					if len(parts) == 2 {
						if trimmed := strings.Trim(strings.TrimSpace(parts[1]), "\""); trimmed != "" {
							return trimmed, nil
						}
					}
				}
			}
		}
	case "windows":
		output, err := exec.Command("reg", "query", "HKEY_LOCAL_MACHINE\\SOFTWARE\\Microsoft\\Cryptography", "/v", "MachineGuid").Output()
		if err == nil {
			for line := range strings.SplitSeq(string(output), "\n") {
				if strings.Contains(line, "MachineGuid") {
					fields := strings.Fields(line)
					if len(fields) >= 3 {
						return fields[len(fields)-1], nil
					}
				}
			}
		}
	}

	if hostname, err := os.Hostname(); err == nil && strings.TrimSpace(hostname) != "" {
		return hostname, nil
	}
	return "", fmt.Errorf("could not determine machine identity")
}
