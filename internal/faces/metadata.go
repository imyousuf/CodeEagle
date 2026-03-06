package faces

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rwcarlsen/goexif/exif"
)

// DateSource indicates how the date was determined.
type DateSource string

const (
	DateSourceEXIF     DateSource = "exif"
	DateSourceFolder   DateSource = "folder"
	DateSourceFilename DateSource = "filename"
	DateSourceMtime    DateSource = "mtime"
	DateSourceUnknown  DateSource = "unknown"
)

// ImageDateResult holds the extracted date and metadata from an image.
type ImageDateResult struct {
	DateTaken   time.Time
	DateSource  DateSource
	FolderName  string
	CameraModel string
	GPSLat      float64
	GPSLon      float64
}

var (
	// folderDatePattern matches folder names ending with _YYYYMMDD or -YYYYMMDD.
	folderDatePattern = regexp.MustCompile(`[_-](\d{4})(\d{2})(\d{2})$`)
	// filenameDatePattern matches IMG_YYYYMMDD_*.
	filenameDatePattern = regexp.MustCompile(`(?i)^(?:IMG|DSC|DCIM|PHOTO|VID)[_-](\d{4})(\d{2})(\d{2})[_-]`)
)

// ExtractImageDate extracts the date taken and metadata from an image file.
// Priority: EXIF DateTimeOriginal > folder name date > filename date > file mtime.
func ExtractImageDate(imagePath string) ImageDateResult {
	result := ImageDateResult{
		FolderName: filepath.Base(filepath.Dir(imagePath)),
	}

	// Try EXIF first.
	if exifDate, model, lat, lon, ok := extractEXIF(imagePath); ok {
		result.DateTaken = exifDate
		result.DateSource = DateSourceEXIF
		result.CameraModel = model
		result.GPSLat = lat
		result.GPSLon = lon
		return result
	}

	// Try folder name date pattern.
	if folderDate, ok := parseFolderDate(result.FolderName); ok {
		result.DateTaken = folderDate
		result.DateSource = DateSourceFolder
		return result
	}

	// Try filename date pattern.
	baseName := filepath.Base(imagePath)
	if fileDate, ok := parseFilenameDate(baseName); ok {
		result.DateTaken = fileDate
		result.DateSource = DateSourceFilename
		return result
	}

	// Fall back to file modification time.
	if info, err := os.Stat(imagePath); err == nil {
		result.DateTaken = info.ModTime()
		result.DateSource = DateSourceMtime
		return result
	}

	result.DateSource = DateSourceUnknown
	return result
}

// extractEXIF tries to read EXIF DateTimeOriginal, camera model, and GPS from a file.
func extractEXIF(imagePath string) (dateTaken time.Time, model string, lat, lon float64, ok bool) {
	f, err := os.Open(imagePath)
	if err != nil {
		return
	}
	defer f.Close()

	x, err := exif.Decode(f)
	if err != nil {
		return
	}

	// DateTimeOriginal.
	dt, err := x.DateTime()
	if err != nil {
		return
	}
	dateTaken = dt
	ok = true

	// Camera model (best-effort).
	if tag, err := x.Get(exif.Model); err == nil {
		model = strings.Trim(tag.String(), "\"")
	}

	// GPS (best-effort).
	if la, lo, err := x.LatLong(); err == nil {
		lat = la
		lon = lo
	}

	return
}

// parseFolderDate extracts a date from a folder name ending in _YYYYMMDD or -YYYYMMDD.
func parseFolderDate(folderName string) (time.Time, bool) {
	matches := folderDatePattern.FindStringSubmatch(folderName)
	if len(matches) != 4 {
		return time.Time{}, false
	}
	return parseYMD(matches[1], matches[2], matches[3])
}

// parseFilenameDate extracts a date from filenames like IMG_20240115_*.
func parseFilenameDate(filename string) (time.Time, bool) {
	matches := filenameDatePattern.FindStringSubmatch(filename)
	if len(matches) != 4 {
		return time.Time{}, false
	}
	return parseYMD(matches[1], matches[2], matches[3])
}

// parseYMD parses year/month/day strings into a time.Time.
func parseYMD(year, month, day string) (time.Time, bool) {
	t, err := time.Parse("20060102", year+month+day)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
