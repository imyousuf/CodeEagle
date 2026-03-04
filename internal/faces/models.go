//go:build faces

package faces

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
	FaceDetectorProto = "deploy.prototxt"
	FaceDetectorModel = "res10_300x300_ssd_iter_140000.caffemodel"

	// SFace face recognizer (ONNX) — ~37 MB, 128-dim L2-normalized embeddings.
	SFaceModel = "face_recognition_sface_2021dec.onnx"
)

// ModelURLs maps model filenames to their download URLs.
var ModelURLs = map[string]string{
	FaceDetectorProto: "https://raw.githubusercontent.com/opencv/opencv/4.x/samples/dnn/face_detector/deploy.prototxt",
	FaceDetectorModel: "https://github.com/opencv/opencv_3rdparty/raw/dnn_samples_face_detector_20170830/res10_300x300_ssd_iter_140000.caffemodel",
	SFaceModel:        "https://github.com/opencv/opencv_zoo/raw/main/models/face_recognition_sface/face_recognition_sface_2021dec.onnx",
}

// EnsureModels downloads any missing model files to modelDir.
func EnsureModels(modelDir string, logFn func(format string, args ...any)) error {
	if logFn == nil {
		logFn = func(string, ...any) {}
	}

	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		return fmt.Errorf("create model dir: %w", err)
	}

	for name, url := range ModelURLs {
		path := filepath.Join(modelDir, name)
		if _, err := os.Stat(path); err == nil {
			logFn("  model %s (already downloaded)", name)
			continue
		}

		logFn("  downloading %s...", name)
		if err := downloadFile(url, path); err != nil {
			return fmt.Errorf("download %s: %w", name, err)
		}

		info, _ := os.Stat(path)
		logFn("  model %s (%s)", name, humanSize(info.Size()))
	}

	return nil
}

// downloadFile downloads a URL to a local file, following redirects.
func downloadFile(url, destPath string) error {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

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

func humanSize(bytes int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
	)
	switch {
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}
