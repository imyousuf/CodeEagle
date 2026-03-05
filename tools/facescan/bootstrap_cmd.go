package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// eventFolder represents a photo event folder with parsed date.
type eventFolder struct {
	Path string
	Name string
	Date time.Time
}

// bootstrapResult tracks per-event bootstrap outcomes.
type bootstrapResult struct {
	EventName         string
	EventDate         time.Time
	TotalFaces        int
	Accepted          int // auto-accepted as exemplars
	Review            int // confidence between reject and accept thresholds
	Rejected          int // below reject threshold
	PerPersonAccepted map[string]int
}

// cmdBootstrap runs chronological bootstrapping.
//
// Usage: facescan bootstrap --seed <clusters-dir> --test <dir1> [dir2...] --auto-accept 0.55 --labels hamza,mahdi [--dry-run]
func cmdBootstrap(args []string) {
	var seedDir string
	var testDirs []string
	var labelFilter []string
	autoAccept := float32(0.55)
	rejectThreshold := float32(0.30)
	maxPerPersonPerEvent := 10
	k := 7
	classifyThreshold := float32(0.35)
	detThreshold := float32(0.5)
	minFace := 30
	maxRes := 1600
	dryRun := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--seed":
			i++
			seedDir = expandPath(args[i])
		case "--test":
			i++
			for i < len(args) && !strings.HasPrefix(args[i], "--") {
				testDirs = append(testDirs, expandPath(args[i]))
				i++
			}
			i--
		case "--labels":
			i++
			labelFilter = strings.Split(strings.ToLower(args[i]), ",")
		case "--auto-accept":
			i++
			fmt.Sscanf(args[i], "%f", &autoAccept)
		case "--reject":
			i++
			fmt.Sscanf(args[i], "%f", &rejectThreshold)
		case "--max-per-event":
			i++
			fmt.Sscanf(args[i], "%d", &maxPerPersonPerEvent)
		case "--k":
			i++
			fmt.Sscanf(args[i], "%d", &k)
		case "--threshold":
			i++
			fmt.Sscanf(args[i], "%f", &classifyThreshold)
		case "--det-threshold":
			i++
			fmt.Sscanf(args[i], "%f", &detThreshold)
		case "--dry-run":
			dryRun = true
		default:
			testDirs = append(testDirs, expandPath(args[i]))
		}
	}

	if seedDir == "" {
		fmt.Fprintln(os.Stderr, "Error: --seed <clusters-dir> required")
		os.Exit(1)
	}
	if len(testDirs) == 0 {
		fmt.Fprintln(os.Stderr, "Error: --test <dir> or positional dirs required")
		os.Exit(1)
	}

	// Build label filter set.
	filterSet := make(map[string]bool)
	for _, l := range labelFilter {
		filterSet[l] = true
	}
	filterAll := len(filterSet) == 0

	// Step 1: Load seed exemplars.
	fmt.Println("=== Loading Seed Exemplars ===")
	seedExemplars, err := loadExemplars(seedDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	labelCounts := make(map[string]int)
	for _, ex := range seedExemplars {
		labelCounts[ex.Label]++
	}
	for label, count := range labelCounts {
		fmt.Printf("  %-15s %d seed exemplars\n", label, count)
	}
	fmt.Printf("  Total: %d seed exemplars\n\n", len(seedExemplars))

	// Step 2: Parse and sort event folders chronologically.
	fmt.Println("=== Sorting Events Chronologically ===")
	events := parseEventFolders(testDirs)
	fmt.Printf("  Found %d events with dates\n", len(events))
	for i, ev := range events {
		imgCount := countImages(ev.Path)
		fmt.Printf("  %3d. %s (%s) — %d images\n", i+1, ev.Name, ev.Date.Format("2006-01-02"), imgCount)
	}
	fmt.Println()

	// Step 3: Initialize scanner.
	fmt.Println("=== Initializing Scanner ===")
	if err := ensureModels(modelsPath()); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	scanner, err := NewScanner(modelsPath(), detThreshold, minFace, maxRes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer scanner.Close()

	// Step 4: Run the bootstrapping loop.
	if dryRun {
		fmt.Println("=== DRY RUN — no exemplars will be added ===")
	}
	fmt.Printf("=== Bootstrapping (auto-accept=%.2f, reject=%.2f, K=%d, max/person/event=%d) ===\n\n",
		autoAccept, rejectThreshold, k, maxPerPersonPerEvent)

	// Copy seed exemplars into the working pool.
	pool := make([]exemplar, len(seedExemplars))
	copy(pool, seedExemplars)

	var results []bootstrapResult
	totalAutoAccepted := 0

	for _, event := range events {
		images, err := collectImages(event.Path)
		if err != nil || len(images) == 0 {
			continue
		}

		result := bootstrapResult{
			EventName:         event.Name,
			EventDate:         event.Date,
			PerPersonAccepted: make(map[string]int),
		}

		// Detect all faces in this event.
		type detectedFace struct {
			record    FaceRecord
			imagePath string
		}
		var eventFaces []detectedFace
		for _, imgPath := range images {
			faces, err := scanner.DetectFaces(imgPath)
			if err != nil {
				continue
			}
			for _, f := range faces {
				eventFaces = append(eventFaces, detectedFace{record: f, imagePath: imgPath})
			}
		}
		result.TotalFaces = len(eventFaces)

		if len(eventFaces) == 0 {
			results = append(results, result)
			continue
		}

		// Classify each face.
		type classifiedFace struct {
			face   detectedFace
			result knnResult
		}
		var classified []classifiedFace
		for _, df := range eventFaces {
			knnRes := classifyKNN(df.record.Embedding, df.imagePath, pool, k, classifyThreshold)
			classified = append(classified, classifiedFace{face: df, result: knnRes})
		}

		// Partition: accepted / review / rejected.
		// Also enforce same-photo constraint: if two faces in same image get same label,
		// keep the higher confidence one.
		type acceptCandidate struct {
			cf         classifiedFace
			confidence float32
		}

		// Group by image for same-photo constraint.
		imageAssignments := make(map[string]map[string]*acceptCandidate) // imagePath → label → best candidate

		var accepted []acceptCandidate
		for _, cf := range classified {
			label := cf.result.Label
			conf := cf.result.Confidence

			if label == "unknown" || conf < rejectThreshold {
				result.Rejected++
				continue
			}

			if !filterAll && !filterSet[label] {
				// Not in the label filter — skip for exemplar purposes.
				continue
			}

			if conf < autoAccept {
				result.Review++
				continue
			}

			// Same-photo constraint check.
			imgPath := cf.face.imagePath
			if imageAssignments[imgPath] == nil {
				imageAssignments[imgPath] = make(map[string]*acceptCandidate)
			}

			existing := imageAssignments[imgPath][label]
			cand := &acceptCandidate{cf: cf, confidence: conf}

			if existing == nil {
				imageAssignments[imgPath][label] = cand
				accepted = append(accepted, *cand)
			} else if conf > existing.confidence {
				// Replace: demote existing to review.
				result.Review++
				result.Accepted-- // undo the previous accept count
				existing.cf = cf
				existing.confidence = conf
				// Replace in accepted list.
				for i, a := range accepted {
					if a.cf.face.imagePath == imgPath && a.cf.result.Label == label {
						accepted[i] = *cand
						break
					}
				}
			} else {
				// This one is weaker — demote to review.
				result.Review++
				continue
			}

			result.Accepted++
		}

		// Apply per-person-per-event cap.
		// Sort by confidence (descending) to keep the best.
		sort.Slice(accepted, func(i, j int) bool {
			return accepted[i].confidence > accepted[j].confidence
		})

		personCount := make(map[string]int)
		var finalAccepted []acceptCandidate
		for _, a := range accepted {
			label := a.cf.result.Label
			if personCount[label] >= maxPerPersonPerEvent {
				result.Accepted--
				result.Review++
				continue
			}
			personCount[label]++
			finalAccepted = append(finalAccepted, a)
		}

		// Add accepted faces to the exemplar pool.
		for _, a := range finalAccepted {
			label := a.cf.result.Label
			result.PerPersonAccepted[label]++

			if !dryRun {
				pool = append(pool, exemplar{
					Label:     label,
					Embedding: a.cf.face.record.Embedding,
					FileName:  fmt.Sprintf("%s:face_%d", a.cf.face.imagePath, a.cf.face.record.FaceIdx),
				})
			}
		}

		totalAutoAccepted += len(finalAccepted)
		results = append(results, result)

		// Print event summary.
		personStrs := make([]string, 0, len(result.PerPersonAccepted))
		for label, count := range result.PerPersonAccepted {
			personStrs = append(personStrs, fmt.Sprintf("%s:%d", label, count))
		}
		sort.Strings(personStrs)

		fmt.Printf("  %-45s %s faces=%d accepted=%d review=%d rejected=%d [%s]\n",
			event.Name, event.Date.Format("2006-01"), result.TotalFaces,
			result.Accepted, result.Review, result.Rejected,
			strings.Join(personStrs, " "))
	}

	// Final summary.
	fmt.Printf("\n=== Summary ===\n")
	fmt.Printf("  Seed exemplars:        %d\n", len(seedExemplars))
	fmt.Printf("  Auto-accepted:         %d\n", totalAutoAccepted)
	fmt.Printf("  Final pool size:       %d\n", len(pool))
	fmt.Printf("  Events processed:      %d\n", len(results))

	if dryRun {
		fmt.Println("\n  (DRY RUN — no exemplars were actually added)")
	}

	// Per-person summary.
	fmt.Println("\n  Per-person pool growth:")
	finalLabelCounts := make(map[string]int)
	for _, ex := range pool {
		finalLabelCounts[ex.Label]++
	}
	sortedLabels := make([]string, 0, len(finalLabelCounts))
	for l := range finalLabelCounts {
		sortedLabels = append(sortedLabels, l)
	}
	sort.Strings(sortedLabels)
	for _, label := range sortedLabels {
		seed := labelCounts[label]
		total := finalLabelCounts[label]
		added := total - seed
		fmt.Printf("    %-15s seed=%d auto=%d total=%d\n", label, seed, added, total)
	}

	// Confidence trend per person across events (for decay detection).
	if len(labelFilter) > 0 {
		fmt.Println("\n  Confidence trend (filtered labels):")
		for _, label := range labelFilter {
			fmt.Printf("    %s: ", label)
			for _, r := range results {
				if r.PerPersonAccepted[label] > 0 {
					// Compute avg confidence for accepted faces of this person in this event.
					fmt.Printf("%s(+%d) ", r.EventDate.Format("2006-01"), r.PerPersonAccepted[label])
				}
			}
			fmt.Println()
		}
	}
}

// datePattern matches trailing _YYYYMMDD or -YYYYMMDD in folder names.
var datePattern = regexp.MustCompile(`[_-](\d{4})(\d{2})(\d{2})$`)

// datePatternShort matches trailing _YYYYMM.
var datePatternShort = regexp.MustCompile(`[_-](\d{4})(\d{2})$`)

// parseEventFolders parses dates from folder names and sorts chronologically.
func parseEventFolders(dirs []string) []eventFolder {
	var events []eventFolder

	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}

		name := filepath.Base(dir)
		date := parseDateFromFolderName(name)
		if date.IsZero() {
			continue
		}

		events = append(events, eventFolder{
			Path: dir,
			Name: name,
			Date: date,
		})
	}

	// Sort by date.
	sort.Slice(events, func(i, j int) bool {
		return events[i].Date.Before(events[j].Date)
	})

	return events
}

// parseDateFromFolderName extracts a date from folder naming patterns.
func parseDateFromFolderName(name string) time.Time {
	// Try YYYYMMDD.
	if m := datePattern.FindStringSubmatch(name); m != nil {
		year, month, day := m[1], m[2], m[3]
		t, err := time.Parse("20060102", year+month+day)
		if err == nil {
			return t
		}
	}

	// Try YYYYMM.
	if m := datePatternShort.FindStringSubmatch(name); m != nil {
		year, month := m[1], m[2]
		t, err := time.Parse("200601", year+month)
		if err == nil {
			return t
		}
	}

	return time.Time{}
}

// countImages counts image files in a directory (non-recursive for speed).
func countImages(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if !e.IsDir() && isImageFile(e.Name()) {
			count++
		}
	}
	return count
}
