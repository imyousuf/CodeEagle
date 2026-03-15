//go:build faces

// Command facetest runs face detection on a single image and prints results.
// Useful for debugging crashes and validating detector behavior.
//
// Usage:
//
//	go run -tags faces ./cmd/facetest <image-path> [model-dir]
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/imyousuf/CodeEagle/internal/faces"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: facetest <image-path> [model-dir]")
		os.Exit(1)
	}

	imagePath := os.Args[1]
	info, err := os.Stat(imagePath)
	if err != nil {
		fmt.Printf("cannot stat %s: %v\n", imagePath, err)
		os.Exit(1)
	}
	fmt.Printf("Image: %s (%d bytes)\n", imagePath, info.Size())

	// Model dir: CLI arg, or ~/.CodeEagle/models/
	modelDir := ""
	if len(os.Args) >= 3 {
		modelDir = os.Args[2]
	} else {
		homeDir, _ := os.UserHomeDir()
		modelDir = filepath.Join(homeDir, ".CodeEagle", "models")
	}
	if err := faces.EnsureModels(modelDir, func(format string, args ...any) {
		fmt.Printf(format+"\n", args...)
	}); err != nil {
		fmt.Printf("model setup failed: %v\n", err)
		os.Exit(1)
	}

	detector, err := faces.NewDetector(modelDir, 0.5, 20, 1024)
	if err != nil {
		fmt.Printf("failed to create detector: %v\n", err)
		os.Exit(1)
	}
	defer detector.Close()

	fmt.Println("Running face detection...")
	result, err := detector.Detect(imagePath)
	if err != nil {
		fmt.Printf("detection failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Faces detected: %d\n", len(result.Faces))
	for i, face := range result.Faces {
		fmt.Printf("  Face %d: bbox=%v confidence=%.4f embedding_len=%d\n",
			i, face.BBox, face.Confidence, len(face.Embedding))
	}
	fmt.Println("OK")
}
