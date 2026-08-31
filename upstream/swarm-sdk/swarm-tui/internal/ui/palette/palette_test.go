package palette

import (
	"fmt"
	"math"
	"strconv"
	"testing"
)

func TestDefaultDarkNeutralTextContrast(t *testing.T) {
	const normalTextMinimum = 4.5

	textColors := []struct {
		name  string
		color string
	}{
		{name: "TextMuted", color: TextMuted},
		{name: "TextDim", color: TextDim},
		{name: "Accent", color: Accent},
		{name: "Info", color: Info},
		{name: "Success", color: Success},
		{name: "Warning", color: Warning},
		{name: "Error", color: Error},
	}
	for _, textColor := range textColors {
		contrast := contrastRatio(textColor.color, Surface)
		if contrast < normalTextMinimum {
			t.Errorf(
				"%s contrast on Surface = %.2f, want >= %.2f",
				textColor.name,
				contrast,
				normalTextMinimum,
			)
		}
	}

	mutedContrast := contrastRatio(TextMuted, Surface)
	dimContrast := contrastRatio(TextDim, Surface)
	if dimContrast <= mutedContrast {
		t.Fatalf(
			"TextDim contrast %.2f should remain stronger than TextMuted %.2f",
			dimContrast,
			mutedContrast,
		)
	}
}

func contrastRatio(foreground, background string) float64 {
	lighter := relativeLuminance(foreground)
	darker := relativeLuminance(background)
	if lighter < darker {
		lighter, darker = darker, lighter
	}
	return (lighter + 0.05) / (darker + 0.05)
}

func relativeLuminance(hex string) float64 {
	var channels [3]float64
	for i := range channels {
		start := 1 + i*2
		value, err := strconv.ParseUint(hex[start:start+2], 16, 8)
		if err != nil {
			panic(fmt.Sprintf("invalid palette color %q: %v", hex, err))
		}
		channel := float64(value) / 255
		if channel <= 0.04045 {
			channels[i] = channel / 12.92
		} else {
			channels[i] = math.Pow((channel+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*channels[0] + 0.7152*channels[1] + 0.0722*channels[2]
}
