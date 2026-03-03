package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Model filenames.
const (
	// SSD face detector (Caffe model) — ~10 MB, well-tested with OpenCV DNN.
	faceDetectorProto  = "deploy.prototxt"
	faceDetectorModel  = "res10_300x300_ssd_iter_140000.caffemodel"

	// SFace face recognizer (ONNX) — ~37 MB, 128-dim L2-normalized embeddings.
	sfaceModel = "face_recognition_sface_2021dec.onnx"
)

// Model download URLs.
var modelURLs = map[string]string{
	faceDetectorProto: "https://raw.githubusercontent.com/opencv/opencv/4.x/samples/dnn/face_detector/deploy.prototxt",
	faceDetectorModel: "https://github.com/opencv/opencv_3rdparty/raw/dnn_samples_face_detector_20170830/res10_300x300_ssd_iter_140000.caffemodel",
	sfaceModel:        "https://github.com/opencv/opencv_zoo/raw/main/models/face_recognition_sface/face_recognition_sface_2021dec.onnx",
}

// ensureModels downloads any missing model files to modelDir.
func ensureModels(modelDir string) error {
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		return fmt.Errorf("create model dir: %w", err)
	}

	for name, url := range modelURLs {
		path := filepath.Join(modelDir, name)
		if _, err := os.Stat(path); err == nil {
			fmt.Printf("  ✓ %s (already downloaded)\n", name)
			continue
		}

		fmt.Printf("  ↓ Downloading %s...\n", name)
		if err := downloadFile(url, path); err != nil {
			return fmt.Errorf("download %s: %w", name, err)
		}

		info, _ := os.Stat(path)
		fmt.Printf("  ✓ %s (%s)\n", name, humanSize(info.Size()))
	}

	return nil
}

// downloadFile downloads a URL to a local file, following redirects.
func downloadFile(url, destPath string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	// Write to temp file first, then rename (atomic).
	tmpPath := destPath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	written, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil {
		os.Remove(tmpPath)
		return err
	}

	if written == 0 {
		os.Remove(tmpPath)
		return fmt.Errorf("downloaded 0 bytes (empty response)")
	}

	return os.Rename(tmpPath, destPath)
}

// humanSize formats bytes into a human-readable string.
func humanSize(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
	)
	switch {
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
