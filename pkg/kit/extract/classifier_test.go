package extract

import "testing"

func TestClassify(t *testing.T) {
	excludeExts := []string{".lock", ".min.js", ".wasm"}

	tests := []struct {
		name     string
		filePath string
		want     FileClass
	}{
		// Known text extensions
		{"text file", "docs/README.txt", FileClassText},
		{"json", "config.json", FileClassText},
		{"csv", "data.csv", FileClassText},
		{"log", "output.log", FileClassText},
		{"sql", "schema.sql", FileClassText},
		{"c source", "main.c", FileClassText},
		{"ini config", "settings.ini", FileClassText},
		// Image extensions
		{"png image", "photo.png", FileClassImage},
		{"jpg image", "photo.jpg", FileClassImage},
		{"jpeg image", "photo.jpeg", FileClassImage},
		{"gif image", "icon.gif", FileClassImage},
		{"webp image", "hero.webp", FileClassImage},
		{"bmp image", "scan.bmp", FileClassImage},
		{"tiff image", "raw.tiff", FileClassImage},
		{"upper case PNG", "photo.PNG", FileClassImage},
		// Document extensions
		{"pdf doc", "report.pdf", FileClassDocument},
		{"docx doc", "plan.docx", FileClassDocument},
		// Exclude list
		{"excluded lock", "package-lock.lock", FileClassSkip},
		{"excluded min.js", "bundle.min.js", FileClassSkip},
		{"excluded wasm", "module.wasm", FileClassSkip},
		// Unknown extensions — whitelist rejects
		{"no extension", "README", FileClassSkip},
		{"video mov", "vacation.mov", FileClassSkip},
		{"video mp4", "clip.mp4", FileClassSkip},
		{"audio mp3", "song.mp3", FileClassSkip},
		{"raw cr3", "IMG_001.CR3", FileClassSkip},
		{"raw cr2", "IMG_002.CR2", FileClassSkip},
		{"raw nef", "DSC_003.NEF", FileClassSkip},
		{"binary exe", "app.exe", FileClassSkip},
		{"archive zip", "backup.zip", FileClassSkip},
		{"unknown ext", "data.xyz", FileClassSkip},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.filePath, excludeExts)
			if got != tt.want {
				t.Errorf("Classify(%q) = %d, want %d", tt.filePath, got, tt.want)
			}
		})
	}
}

func TestClassifyContent(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		want    FileClass
	}{
		{"empty file", []byte{}, FileClassText},
		{"text content", []byte("hello world"), FileClassText},
		{"binary with null", []byte("hello\x00world"), FileClassSkip},
		{"utf8 text", []byte("こんにちは"), FileClassText},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyContent(tt.content)
			if got != tt.want {
				t.Errorf("ClassifyContent() = %d, want %d", got, tt.want)
			}
		})
	}
}
