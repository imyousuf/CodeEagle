package generic

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"strings"

	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

// downscaleImage decodes an image, scales the longest edge to maxRes,
// composites RGBA onto a white background (JPEG doesn't support alpha),
// and re-encodes as JPEG. Returns the downscaled bytes and dimensions.
//
// If the image is already smaller than maxRes, it is still re-encoded
// as JPEG to normalize the format for LLM consumption.
func downscaleImage(data []byte, mimeType string, maxRes int) ([]byte, int, int, error) {
	if maxRes <= 0 {
		maxRes = 1024
	}

	src, err := decodeImage(data, mimeType)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("decode image: %w", err)
	}

	bounds := src.Bounds()
	origW := bounds.Dx()
	origH := bounds.Dy()

	// Calculate new dimensions preserving aspect ratio.
	newW, newH := origW, origH
	longest := origW
	if origH > longest {
		longest = origH
	}
	if longest > maxRes {
		scale := float64(maxRes) / float64(longest)
		newW = int(float64(origW) * scale)
		newH = int(float64(origH) * scale)
		if newW < 1 {
			newW = 1
		}
		if newH < 1 {
			newH = 1
		}
	}

	// Create destination image with white background (for RGBA→RGB).
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	// Fill white.
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	for y := range newH {
		for x := range newW {
			dst.Set(x, y, white)
		}
	}

	// Scale the image using BiLinear interpolation.
	draw.BiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)

	// Encode as JPEG.
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
		return nil, 0, 0, fmt.Errorf("encode jpeg: %w", err)
	}

	return buf.Bytes(), newW, newH, nil
}

// decodeImage decodes image data based on MIME type.
func decodeImage(data []byte, mimeType string) (image.Image, error) {
	r := bytes.NewReader(data)
	mt := strings.ToLower(mimeType)

	switch {
	case strings.Contains(mt, "png"):
		return png.Decode(r)
	case strings.Contains(mt, "jpeg") || strings.Contains(mt, "jpg"):
		return jpeg.Decode(r)
	case strings.Contains(mt, "gif"):
		return gif.Decode(r)
	case strings.Contains(mt, "webp"):
		return webp.Decode(r)
	case strings.Contains(mt, "bmp"):
		img, _, err := image.Decode(r)
		if err != nil {
			return nil, err
		}
		return img, nil
	default:
		// Try generic decode (relies on registered decoders).
		img, _, err := image.Decode(r)
		if err != nil {
			return nil, err
		}
		return img, nil
	}
}
