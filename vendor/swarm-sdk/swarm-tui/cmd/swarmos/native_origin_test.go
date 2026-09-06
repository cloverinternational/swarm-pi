package main

import "testing"

func TestIsNativeGatewayOrigin(t *testing.T) {
	for _, origin := range []string{
		"http://tauri.localhost",
		"https://tauri.localhost",
		"http://localhost",
		"http://localhost:1420",
	} {
		if !isNativeGatewayOrigin(origin) {
			t.Errorf("isNativeGatewayOrigin(%q)=false, want true", origin)
		}
	}
	for _, origin := range []string{
		"http://evil.example",
		"https://tauri.localhost.evil.example",
		"ws://tauri.localhost",
		"not a URL",
	} {
		if isNativeGatewayOrigin(origin) {
			t.Errorf("isNativeGatewayOrigin(%q)=true, want false", origin)
		}
	}
}
