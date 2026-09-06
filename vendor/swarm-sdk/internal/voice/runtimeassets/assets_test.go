package runtimeassets

import (
	"strings"
	"testing"
)

func TestBundledServerImplementsOpenAIMultipartTranscriptionContract(t *testing.T) {
	server := string(Files["server.py"])
	for _, required := range []string{
		`@app.options("/v1/audio/transcriptions")`,
		`@app.post("/v1/audio/transcriptions")`,
		`File(...)`,
		`alias="model"`,
		`model_name and model_name != MODEL_ID`,
		`{"text": text, "model": MODEL_ID}`,
		`Form("json")`,
		`"X-Swarm-Transcription-Contract"] = "openai-multipart-v1"`,
	} {
		if !strings.Contains(server, required) {
			t.Errorf("server.py missing %q", required)
		}
	}
	if strings.Contains(server, `@app.post("/transcribe")`) {
		t.Fatal("server.py still exposes the legacy transcription route")
	}
}

func TestPinnedBundleIdentityIsConsistent(t *testing.T) {
	compose := string(Files["compose.yaml"])
	if strings.Contains(compose, ":latest") {
		t.Fatal("compose image must remain pinned")
	}
	for _, required := range []string{
		"image: " + Image,
		`"127.0.0.1:${NEMO_PORT}:8001"`,
		"NEMO_MODEL: ${NEMO_MODEL}",
		"com.swarm.voice.runtime: " + BundleVersion,
	} {
		if !strings.Contains(compose, required) {
			t.Fatalf("compose bundle missing %q", required)
		}
	}
	if ManagedLabel != "com.swarm.voice.runtime="+BundleVersion {
		t.Fatalf("managed label %q does not match bundle version %q", ManagedLabel, BundleVersion)
	}
}
