package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"

	"github.com/BourgeoisBear/rasterm"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"golang.org/x/term"
)

// cmdImageView displays an image in the terminal.
// Tries kitty/sixel/iTerm2 graphics protocols for full-resolution rendering,
// falls back to tview's Unicode block character approximation.
// Usage: facescan imageview <path> [--tview]
func cmdImageView(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: facescan imageview <image-path> [--tview]")
		os.Exit(1)
	}

	forceTview := false
	imgPath := ""
	for _, a := range args {
		if a == "--tview" {
			forceTview = true
		} else {
			imgPath = expandPath(a)
		}
	}

	if imgPath == "" {
		fmt.Fprintln(os.Stderr, "Usage: facescan imageview <image-path> [--tview]")
		os.Exit(1)
	}

	// Load the image.
	f, err := os.Open(imgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening image: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	img, format, err := image.Decode(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error decoding image: %v\n", err)
		os.Exit(1)
	}

	bounds := img.Bounds()

	if !forceTview {
		if rendered := renderProtocol(img, imgPath, format, bounds); rendered {
			return
		}
	}

	// Fallback: tview Unicode block character rendering.
	renderTview(img, imgPath, format, bounds)
}

// renderProtocol attempts kitty, sixel, or iTerm2 image rendering.
// Returns true if successful.
func renderProtocol(img image.Image, imgPath, format string, bounds image.Rectangle) bool {
	// Report detection results for all protocols.
	kittyOK := rasterm.IsKittyCapable()
	sixelOK, sixelErr := rasterm.IsSixelCapable()
	itermOK := rasterm.IsItermCapable()

	fmt.Fprintf(os.Stderr, "Terminal protocol detection:\n")
	fmt.Fprintf(os.Stderr, "  Kitty:  %v\n", kittyOK)
	if sixelErr != nil {
		fmt.Fprintf(os.Stderr, "  Sixel:  %v (error: %v)\n", sixelOK, sixelErr)
	} else {
		fmt.Fprintf(os.Stderr, "  Sixel:  %v\n", sixelOK)
	}
	fmt.Fprintf(os.Stderr, "  iTerm2: %v\n", itermOK)

	// Report relevant env vars for debugging.
	envKeys := []string{"TERM", "TERM_PROGRAM", "LC_TERMINAL", "KITTY_WINDOW_ID"}
	for _, k := range envKeys {
		if v := os.Getenv(k); v != "" {
			fmt.Fprintf(os.Stderr, "  %s=%s\n", k, v)
		}
	}

	// Scale image to fit terminal.
	scaled := fitToTerminal(img)

	// Try kitty protocol.
	if kittyOK {
		fmt.Fprintf(os.Stderr, "\nUsing kitty protocol for %s (%s, %dx%d)\n", shortPath(imgPath), format, bounds.Dx(), bounds.Dy())
		if err := rasterm.KittyWriteImage(os.Stdout, scaled, rasterm.KittyImgOpts{}); err == nil {
			fmt.Println()
			waitForKey()
			return true
		}
	}

	// Try sixel protocol.
	if sixelOK {
		fmt.Fprintf(os.Stderr, "\nUsing sixel protocol for %s (%s, %dx%d)\n", shortPath(imgPath), format, bounds.Dx(), bounds.Dy())
		paletted := toPaletted(scaled)
		if err := rasterm.SixelWriteImage(os.Stdout, paletted); err == nil {
			fmt.Println()
			waitForKey()
			return true
		}
	}

	// Try iTerm2/WezTerm protocol.
	if itermOK {
		fmt.Fprintf(os.Stderr, "\nUsing iTerm2 protocol for %s (%s, %dx%d)\n", shortPath(imgPath), format, bounds.Dx(), bounds.Dy())
		if err := rasterm.ItermWriteImage(os.Stdout, scaled); err == nil {
			fmt.Println()
			waitForKey()
			return true
		}
	}

	fmt.Fprintf(os.Stderr, "\nNo graphics protocol available, falling back to tview\n")
	return false
}

// toPaletted converts an image to a 256-color paletted image with Floyd-Steinberg dithering.
func toPaletted(img image.Image) *image.Paletted {
	// Build a 256-color palette (6x6x6 color cube + 40 grays).
	palette := make(color.Palette, 0, 256)
	// 6x6x6 color cube = 216 colors.
	for r := 0; r < 6; r++ {
		for g := 0; g < 6; g++ {
			for b := 0; b < 6; b++ {
				palette = append(palette, color.RGBA{
					R: uint8(r * 51), G: uint8(g * 51), B: uint8(b * 51), A: 255,
				})
			}
		}
	}
	// 40 shades of gray.
	for i := 0; i < 40; i++ {
		v := uint8(i * 255 / 39)
		palette = append(palette, color.RGBA{R: v, G: v, B: v, A: 255})
	}

	bounds := img.Bounds()
	paletted := image.NewPaletted(bounds, palette)
	draw.FloydSteinberg.Draw(paletted, bounds, img, bounds.Min)
	return paletted
}

// fitToTerminal scales an image to fit the terminal dimensions.
func fitToTerminal(img image.Image) image.Image {
	width, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 || height <= 0 {
		return img
	}

	bounds := img.Bounds()
	imgW := bounds.Dx()
	imgH := bounds.Dy()

	// Estimate pixel dimensions: each cell is roughly 8px wide, 16px tall.
	termPixW := width * 8
	termPixH := (height - 2) * 16

	scaleW := float64(termPixW) / float64(imgW)
	scaleH := float64(termPixH) / float64(imgH)
	scale := scaleW
	if scaleH < scale {
		scale = scaleH
	}
	if scale >= 1.0 {
		return img
	}

	newW := int(float64(imgW) * scale)
	newH := int(float64(imgH) * scale)
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}

	// Nearest-neighbor resize.
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	for y := 0; y < newH; y++ {
		srcY := y * imgH / newH
		for x := 0; x < newW; x++ {
			srcX := x * imgW / newW
			dst.Set(x, y, img.At(bounds.Min.X+srcX, bounds.Min.Y+srcY))
		}
	}
	return dst
}

// waitForKey waits for a single keypress.
func waitForKey() {
	fmt.Fprintf(os.Stderr, "Press any key to exit...")
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return
	}
	defer term.Restore(fd, oldState)

	buf := make([]byte, 1)
	os.Stdin.Read(buf)
	fmt.Fprintln(os.Stderr)
}

// renderTview uses tview's Unicode block character Image widget as fallback.
func renderTview(img image.Image, imgPath, format string, bounds image.Rectangle) {
	fmt.Printf("Loaded %s (%s, %dx%d) — tview fallback, press Esc or q to quit\n", shortPath(imgPath), format, bounds.Dx(), bounds.Dy())

	app := tview.NewApplication()

	imageView := tview.NewImage()
	imageView.SetImage(img)
	imageView.SetColors(tview.TrueColor)
	imageView.SetDithering(tview.DitheringFloydSteinberg)

	frame := tview.NewFrame(imageView)
	frame.SetBorder(true)
	frame.SetTitle(fmt.Sprintf(" %s (%s, %dx%d) ", shortPath(imgPath), format, bounds.Dx(), bounds.Dy()))
	frame.SetTitleAlign(tview.AlignLeft)
	frame.AddText("Press Esc or q to quit", false, tview.AlignCenter, tcell.ColorGray)

	app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEscape:
			app.Stop()
			return nil
		case tcell.KeyRune:
			if event.Rune() == 'q' {
				app.Stop()
				return nil
			}
		}
		return event
	})

	if err := app.SetRoot(frame, true).Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running UI: %v\n", err)
		os.Exit(1)
	}
}
