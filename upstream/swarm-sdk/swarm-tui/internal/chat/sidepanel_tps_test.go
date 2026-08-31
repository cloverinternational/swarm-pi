package chat

import "testing"

// TestFormatTPSLine covers the side panel speed line in all its states.
func TestFormatTPSLine(t *testing.T) {
	cases := []struct {
		name                     string
		current, avg, peak, hist float64
		streaming                bool
		want                     string
	}{
		{
			name:    "streaming with avg and peak",
			current: 42, avg: 38, peak: 51, hist: 35, streaming: true,
			want: "42 tok/s   avg 38 · peak 51",
		},
		{
			name:    "streaming, peak equals current (skip redundant peak)",
			current: 51, avg: 38, peak: 51, hist: 0, streaming: true,
			want: "51 tok/s   avg 38",
		},
		{
			name:    "streaming but no tokens yet",
			current: 0, avg: 0, peak: 0, hist: 35, streaming: true,
			// Falls through to the idle branches: all-time only.
			want: "35 tok/s all-time",
		},
		{
			name: "idle with session and all-time",
			avg:  38, hist: 35,
			want: "38 tok/s session · 35 all-time",
		},
		{
			name: "idle with session only",
			avg:  38,
			want: "38 tok/s session",
		},
		{
			name: "idle with all-time only",
			hist: 35,
			want: "35 tok/s all-time",
		},
		{
			name: "fresh session, no data at all",
			want: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatTPSLine(c.current, c.avg, c.peak, c.hist, c.streaming)
			if got != c.want {
				t.Errorf("formatTPSLine(%v,%v,%v,%v,%v) = %q, want %q",
					c.current, c.avg, c.peak, c.hist, c.streaming, got, c.want)
			}
		})
	}
}
