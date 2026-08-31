package voice

import "testing"

func TestProviderFactoryCreatesOpenAICompatibleProvider(t *testing.T) {
	factory := NewProviderFactoryWithConfig(&ProviderFactoryConfig{
		DefaultProvider: ProviderOpenAICompatible,
		BaseURLs: map[string]string{
			string(ProviderOpenAICompatible): "http://127.0.0.1:8001",
		},
	})
	provider, err := factory.CreateDefault(
		WithModel("local-model"),
		WithLanguage("en"),
		WithSampleRate(16000),
	)
	if err != nil {
		t.Fatal(err)
	}
	compatible, ok := provider.(*OpenAICompatibleProvider)
	if !ok {
		t.Fatalf("provider type = %T", provider)
	}
	if compatible.model != "local-model" {
		t.Fatalf("model = %q", compatible.model)
	}
	if compatible.endpoint != "http://127.0.0.1:8001/v1/audio/transcriptions" {
		t.Fatalf("endpoint = %q", compatible.endpoint)
	}
}
