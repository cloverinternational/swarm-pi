//go:build !linux && !windows

package main

import "fmt"

func readProcessEvidence(pid int) (start, executable, uid string, err error) {
	return "", "", "", fmt.Errorf("destructive process evidence is unsupported on this platform")
}
