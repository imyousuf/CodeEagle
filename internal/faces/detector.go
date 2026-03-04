//go:build faces

package faces

import (
	"fmt"
	"image"
	"math"
	"path/filepath"
	"sort"
	"strings"

	"gocv.io/x/gocv"
)

// DetectResult holds the output of face and object detection for an image.
type DetectResult struct {
	Faces []FaceRecord
}

// Detector wraps OpenCV DNN models for face detection and embedding extraction.
type Detector struct {
	faceNet  gocv.Net
	sfaceNet gocv.Net
	minFace  int
	maxRes   int
	confThr  float32
}

// rawDetection is a face detection before embedding extraction.
type rawDetection struct {
	x1, y1, x2, y2 int
	confidence     float32
}

// NewDetector creates a Detector with DNN-based face detection and recognition.
func NewDetector(modelDir string, threshold float32, minFace, maxRes int) (*Detector, error) {
	protoPath := filepath.Join(modelDir, FaceDetectorProto)
	modelPath := filepath.Join(modelDir, FaceDetectorModel)
	sfacePath := filepath.Join(modelDir, SFaceModel)

	faceNet := gocv.ReadNetFromCaffe(protoPath, modelPath)
	if faceNet.Empty() {
		return nil, fmt.Errorf("failed to load face detector from %s", modelPath)
	}

	sfaceNet := gocv.ReadNetFromONNX(sfacePath)
	if sfaceNet.Empty() {
		faceNet.Close()
		return nil, fmt.Errorf("failed to load SFace model from %s", sfacePath)
	}

	return &Detector{
		faceNet:  faceNet,
		sfaceNet: sfaceNet,
		minFace:  minFace,
		maxRes:   maxRes,
		confThr:  threshold,
	}, nil
}

// Close releases OpenCV resources.
func (d *Detector) Close() {
	d.faceNet.Close()
	d.sfaceNet.Close()
}

// Detect reads an image, detects faces at multiple scales, and extracts embeddings.
func (d *Detector) Detect(imagePath string) (*DetectResult, error) {
	img := gocv.IMRead(imagePath, gocv.IMReadColor)
	if img.Empty() {
		return nil, fmt.Errorf("failed to read image: %s", imagePath)
	}
	defer img.Close()

	resized, scale := d.resizeImage(img)
	defer resized.Close()

	h := resized.Rows()
	w := resized.Cols()

	// Multi-scale face detection.
	var allDets []rawDetection

	// Pass 1: full image.
	allDets = append(allDets, d.detectInRegion(resized, 0, 0, w, h)...)

	// Pass 2: 2x2 tiles with overlap.
	overlap := 60
	tileW := w/2 + overlap
	tileH := h/2 + overlap
	tileOffsets := [][2]int{
		{0, 0},
		{maxInt(w/2-overlap, 0), 0},
		{0, maxInt(h/2-overlap, 0)},
		{maxInt(w/2-overlap, 0), maxInt(h/2-overlap, 0)},
	}

	for _, off := range tileOffsets {
		tx, ty := off[0], off[1]
		tx2 := minInt(tx+tileW, w)
		ty2 := minInt(ty+tileH, h)
		tile := resized.Region(image.Rect(tx, ty, tx2, ty2))
		tileDets := d.detectInRegion(tile, tx, ty, tx2-tx, ty2-ty)
		tile.Close()
		allDets = append(allDets, tileDets...)
	}

	// Pass 3: 3x3 tiles for large images.
	if w >= 600 && h >= 400 {
		tileW3 := w/3 + overlap
		tileH3 := h/3 + overlap
		for row := range 3 {
			for col := range 3 {
				tx := maxInt(col*w/3-overlap/2, 0)
				ty := maxInt(row*h/3-overlap/2, 0)
				tx2 := minInt(tx+tileW3, w)
				ty2 := minInt(ty+tileH3, h)
				tile := resized.Region(image.Rect(tx, ty, tx2, ty2))
				tileDets := d.detectInRegion(tile, tx, ty, tx2-tx, ty2-ty)
				tile.Close()
				allDets = append(allDets, tileDets...)
			}
		}
	}

	// NMS dedup.
	allDets = nms(allDets, 0.3)

	// Extract embeddings.
	var records []FaceRecord
	for _, det := range allDets {
		faceW := det.x2 - det.x1
		faceH := det.y2 - det.y1
		if faceW < d.minFace || faceH < d.minFace {
			continue
		}

		embedding, err := d.extractEmbedding(resized, det)
		if err != nil {
			continue
		}

		origX1 := int(float32(det.x1) / scale)
		origY1 := int(float32(det.y1) / scale)
		origX2 := int(float32(det.x2) / scale)
		origY2 := int(float32(det.y2) / scale)

		records = append(records, FaceRecord{
			ImagePath:  imagePath,
			FaceIdx:    len(records),
			BBox:       image.Rect(origX1, origY1, origX2, origY2),
			Confidence: det.confidence,
			Embedding:  embedding,
			ClusterID:  0,
		})
	}

	return &DetectResult{Faces: records}, nil
}

func (d *Detector) detectInRegion(region gocv.Mat, offsetX, offsetY, regionW, regionH int) []rawDetection {
	blob := gocv.BlobFromImage(region, 1.0, image.Pt(300, 300),
		gocv.NewScalar(104.0, 177.0, 123.0, 0), false, false)
	defer blob.Close()

	d.faceNet.SetInput(blob, "")
	detection := d.faceNet.Forward("")
	defer detection.Close()

	nDets := detection.Total() / 7
	var dets []rawDetection

	for i := range nDets {
		confidence := detection.GetFloatAt(0, i*7+2)
		if confidence < d.confThr {
			continue
		}

		x1 := detection.GetFloatAt(0, i*7+3)
		y1 := detection.GetFloatAt(0, i*7+4)
		x2 := detection.GetFloatAt(0, i*7+5)
		y2 := detection.GetFloatAt(0, i*7+6)

		px1 := clampInt(int(x1*float32(regionW))+offsetX, 0, offsetX+regionW-1)
		py1 := clampInt(int(y1*float32(regionH))+offsetY, 0, offsetY+regionH-1)
		px2 := clampInt(int(x2*float32(regionW))+offsetX, 0, offsetX+regionW)
		py2 := clampInt(int(y2*float32(regionH))+offsetY, 0, offsetY+regionH)

		if px2-px1 > 0 && py2-py1 > 0 {
			dets = append(dets, rawDetection{
				x1: px1, y1: py1, x2: px2, y2: py2,
				confidence: confidence,
			})
		}
	}

	return dets
}

func (d *Detector) extractEmbedding(img gocv.Mat, det rawDetection) ([]float32, error) {
	h := img.Rows()
	w := img.Cols()

	faceW := det.x2 - det.x1
	faceH := det.y2 - det.y1
	padX := int(float32(faceW) * 0.2)
	padY := int(float32(faceH) * 0.2)

	cropX1 := clampInt(det.x1-padX, 0, w-1)
	cropY1 := clampInt(det.y1-padY, 0, h-1)
	cropX2 := clampInt(det.x2+padX, 0, w)
	cropY2 := clampInt(det.y2+padY, 0, h)

	if cropX2-cropX1 < 10 || cropY2-cropY1 < 10 {
		return nil, fmt.Errorf("face crop too small")
	}

	faceROI := img.Region(image.Rect(cropX1, cropY1, cropX2, cropY2))
	faceMat := gocv.NewMat()
	gocv.Resize(faceROI, &faceMat, image.Pt(112, 112), 0, 0, gocv.InterpolationLinear)
	faceROI.Close()

	faceBlob := gocv.BlobFromImage(faceMat, 1.0, image.Pt(112, 112),
		gocv.NewScalar(0, 0, 0, 0), true, false)
	faceMat.Close()

	d.sfaceNet.SetInput(faceBlob, "")
	feature := d.sfaceNet.Forward("")
	faceBlob.Close()

	embLen := feature.Total()
	if embLen < 128 {
		feature.Close()
		return nil, fmt.Errorf("unexpected embedding length: %d", embLen)
	}

	embedding := make([]float32, 128)
	var norm float32
	for j := range 128 {
		v := feature.GetFloatAt(0, j)
		embedding[j] = v
		norm += v * v
	}
	feature.Close()

	norm = float32(math.Sqrt(float64(norm)))
	if norm > 0 {
		for j := range embedding {
			embedding[j] /= norm
		}
	}

	return embedding, nil
}

func nms(dets []rawDetection, iouThreshold float32) []rawDetection {
	if len(dets) == 0 {
		return nil
	}

	sort.Slice(dets, func(i, j int) bool {
		return dets[i].confidence > dets[j].confidence
	})

	keep := make([]bool, len(dets))
	for i := range keep {
		keep[i] = true
	}

	for i := range len(dets) {
		if !keep[i] {
			continue
		}
		for j := i + 1; j < len(dets); j++ {
			if !keep[j] {
				continue
			}
			if iou(dets[i], dets[j]) > iouThreshold {
				keep[j] = false
			}
		}
	}

	var result []rawDetection
	for i, d := range dets {
		if keep[i] {
			result = append(result, d)
		}
	}
	return result
}

func iou(a, b rawDetection) float32 {
	x1 := maxInt(a.x1, b.x1)
	y1 := maxInt(a.y1, b.y1)
	x2 := minInt(a.x2, b.x2)
	y2 := minInt(a.y2, b.y2)

	if x2 <= x1 || y2 <= y1 {
		return 0
	}

	inter := float32((x2 - x1) * (y2 - y1))
	areaA := float32((a.x2 - a.x1) * (a.y2 - a.y1))
	areaB := float32((b.x2 - b.x1) * (b.y2 - b.y1))

	return inter / (areaA + areaB - inter)
}

func (d *Detector) resizeImage(img gocv.Mat) (gocv.Mat, float32) {
	h := img.Rows()
	w := img.Cols()

	longest := w
	if h > w {
		longest = h
	}

	if longest <= d.maxRes {
		clone := img.Clone()
		return clone, 1.0
	}

	scale := float32(d.maxRes) / float32(longest)
	newW := int(float32(w) * scale)
	newH := int(float32(h) * scale)

	resized := gocv.NewMat()
	gocv.Resize(img, &resized, image.Pt(newW, newH), 0, 0, gocv.InterpolationLinear)

	return resized, scale
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// IsImageFile checks if a path has an image file extension.
func IsImageFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".bmp", ".tiff", ".tif", ".webp":
		return true
	}
	return false
}
