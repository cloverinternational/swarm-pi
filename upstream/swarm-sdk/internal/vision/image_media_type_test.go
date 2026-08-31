package vision

import (
	"encoding/base64"
	"testing"
)

func TestReconcileMediaTypeUsesActualImageBytes(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		declared string
		want     string
	}{
		{
			name:     "jpeg mislabeled as png",
			data:     []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00},
			declared: MediaTypePNG,
			want:     MediaTypeJPEG,
		},
		{
			name:     "png mislabeled as jpeg",
			data:     []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'},
			declared: MediaTypeJPEG,
			want:     MediaTypePNG,
		},
		{
			name:     "correct declaration remains correct",
			data:     []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00},
			declared: MediaTypeJPEG,
			want:     MediaTypeJPEG,
		},
		{
			name:     "unknown bytes preserve declaration",
			data:     []byte("not an image"),
			declared: MediaTypePNG,
			want:     MediaTypePNG,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := base64.StdEncoding.EncodeToString(tt.data)
			if got := ReconcileMediaType(encoded, tt.declared); got != tt.want {
				t.Fatalf("ReconcileMediaType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDetectMediaTypeFromBase64RejectsInvalidData(t *testing.T) {
	if got, ok := DetectMediaTypeFromBase64("not-valid-base64!!!"); ok || got != "" {
		t.Fatalf("DetectMediaTypeFromBase64() = (%q, %v), want (\"\", false)", got, ok)
	}
}
