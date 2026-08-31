package main

import (
	"fmt"
	"image/png"
	"os"
	"strconv"
)

func loadPNG(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return f, nil
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "usage: go run ./cmd/pngdiff <baseline.png> <current.png>\n")
		os.Exit(2)
	}

	maxRatio := 0.0075 // 0.75% pixel variance tolerance for cursor/jitter noise.
	if raw := os.Getenv("PNGDIFF_MAX_CHANGED_RATIO"); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v < 0 || v > 1 {
			fmt.Fprintf(os.Stderr, "invalid PNGDIFF_MAX_CHANGED_RATIO %q (expected 0..1)\n", raw)
			os.Exit(2)
		}
		maxRatio = v
	}

	baselinePath := os.Args[1]
	currentPath := os.Args[2]

	baseFile, err := loadPNG(baselinePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer baseFile.Close()

	currFile, err := loadPNG(currentPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer currFile.Close()

	baseImg, err := png.Decode(baseFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "decode %s: %v\n", baselinePath, err)
		os.Exit(1)
	}
	currImg, err := png.Decode(currFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "decode %s: %v\n", currentPath, err)
		os.Exit(1)
	}

	baseBounds := baseImg.Bounds()
	currBounds := currImg.Bounds()
	if !baseBounds.Eq(currBounds) {
		fmt.Fprintf(os.Stderr, "png diff failed: dimensions differ baseline=%dx%d current=%dx%d\n",
			baseBounds.Dx(), baseBounds.Dy(), currBounds.Dx(), currBounds.Dy())
		os.Exit(1)
	}

	var changed int64
	total := int64(baseBounds.Dx() * baseBounds.Dy())
	for y := baseBounds.Min.Y; y < baseBounds.Max.Y; y++ {
		for x := baseBounds.Min.X; x < baseBounds.Max.X; x++ {
			if baseImg.At(x, y) != currImg.At(x, y) {
				changed++
			}
		}
	}

	ratio := float64(changed) / float64(total)
	if ratio > maxRatio {
		fmt.Fprintf(os.Stderr, "png diff failed: %d/%d pixels changed (%.4f%% > %.4f%%)\n",
			changed, total, ratio*100, maxRatio*100)
		os.Exit(1)
	}

	fmt.Printf("png diff ok: %s vs %s changed=%.4f%% threshold=%.4f%%\n",
		baselinePath, currentPath, ratio*100, maxRatio*100)
}
