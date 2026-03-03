package main

import (
	"fmt"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"

	"gocv.io/x/gocv"
)

func cmdExtract() {
	db, err := LoadDB(dbPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "No scan data found. Run 'facescan scan' first.\n")
		os.Exit(1)
	}

	outDir := filepath.Join(dataPath(), "faces")
	os.RemoveAll(outDir) // clean previous extraction

	// Group faces by cluster.
	clusters := make(map[int][]FaceRecord)
	for _, f := range db.Faces {
		clusters[f.ClusterID] = append(clusters[f.ClusterID], f)
	}

	totalExtracted := 0

	// Process each cluster.
	for clID := -1; clID <= maxClusterID(clusters); clID++ {
		faces, ok := clusters[clID]
		if !ok {
			continue
		}

		// Determine folder name.
		var dirName string
		if clID == -1 {
			dirName = "noise"
		} else {
			label := db.Labels[clID]
			if label != "" {
				dirName = fmt.Sprintf("cluster_%02d_%s", clID, sanitizeName(label))
			} else {
				dirName = fmt.Sprintf("cluster_%02d", clID)
			}
		}

		clusterDir := filepath.Join(outDir, dirName)
		os.MkdirAll(clusterDir, 0o755)

		for _, face := range faces {
			thumbPath := filepath.Join(clusterDir,
				fmt.Sprintf("%s_face%d.jpg",
					strings.TrimSuffix(filepath.Base(face.ImagePath), filepath.Ext(face.ImagePath)),
					face.FaceIdx))

			if err := extractFaceThumb(face.ImagePath, face.BBox, thumbPath); err != nil {
				fmt.Printf("  Warning: %s — %v\n", filepath.Base(face.ImagePath), err)
				continue
			}
			totalExtracted++
		}

		fmt.Printf("  %-30s %d faces\n", dirName+"/", len(faces))
	}

	fmt.Printf("\nExtracted %d face thumbnails to %s\n", totalExtracted, shortPath(outDir))
	fmt.Println("Open the folder to visually verify clusters before labeling.")
}

// extractFaceThumb reads the original image, crops the face with padding, and saves as JPEG.
func extractFaceThumb(imagePath string, bbox image.Rectangle, outPath string) error {
	img := gocv.IMRead(imagePath, gocv.IMReadColor)
	if img.Empty() {
		return fmt.Errorf("cannot read %s", imagePath)
	}
	defer img.Close()

	h := img.Rows()
	w := img.Cols()

	// Add generous padding (40%) around the face for context.
	faceW := bbox.Dx()
	faceH := bbox.Dy()
	padX := int(float32(faceW) * 0.4)
	padY := int(float32(faceH) * 0.4)

	cropX1 := clampInt(bbox.Min.X-padX, 0, w-1)
	cropY1 := clampInt(bbox.Min.Y-padY, 0, h-1)
	cropX2 := clampInt(bbox.Max.X+padX, 0, w)
	cropY2 := clampInt(bbox.Max.Y+padY, 0, h)

	if cropX2-cropX1 < 10 || cropY2-cropY1 < 10 {
		return fmt.Errorf("face crop too small")
	}

	faceROI := img.Region(image.Rect(cropX1, cropY1, cropX2, cropY2))

	// Resize to a standard thumbnail size (256px on longest edge).
	thumbSize := 256
	roiW := cropX2 - cropX1
	roiH := cropY2 - cropY1
	scale := float64(thumbSize) / float64(maxInt(roiW, roiH))
	newW := int(float64(roiW) * scale)
	newH := int(float64(roiH) * scale)

	thumb := gocv.NewMat()
	gocv.Resize(faceROI, &thumb, image.Pt(newW, newH), 0, 0, gocv.InterpolationLinear)
	faceROI.Close()

	// Convert gocv.Mat to Go image and save as JPEG.
	goImg, err := thumb.ToImage()
	thumb.Close()
	if err != nil {
		return fmt.Errorf("convert to image: %w", err)
	}

	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	return jpeg.Encode(f, goImg, &jpeg.Options{Quality: 90})
}

func sanitizeName(name string) string {
	r := strings.NewReplacer(" ", "_", "/", "_", "\\", "_", ":", "_")
	return r.Replace(name)
}
