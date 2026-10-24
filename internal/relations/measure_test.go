package relations

import (
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestCalibrateFindsTheGateThatKeepsPrecision(t *testing.T) {
	probs := []float64{0.95, 0.9, 0.85, 0.8, 0.75, 0.7, 0.65, 0.55, 0.3, 0.2, 0.62, 0.1}
	related := []bool{true, true, true, true, true, true, true, false, false, false, false, false}
	cal := Calibrate(probs, related, 0.95)

	if cal.Pairs != 12 || cal.Related != 7 {
		t.Errorf("counts = %d pairs, %d related", cal.Pairs, cal.Related)
	}
	// Every related pair outscores every unrelated one except the 0.62.
	if cal.AUC < 0.9 || cal.AUC > 1 {
		t.Errorf("AUC = %.3f", cal.AUC)
	}
	if cal.Gate != 0.7 {
		t.Errorf("gate = %.1f, want 0.7 (the first with precision 1.0)", cal.Gate)
	}
	for _, b := range cal.Buckets {
		if b.Gate == 0.6 && math.Abs(b.Precision-7.0/8.0) > 1e-9 {
			t.Errorf("precision at 0.6 = %.3f, want 7/8", b.Precision)
		}
	}
}

func TestAUCOfPerfectAndUselessScores(t *testing.T) {
	if got := auc([]float64{0.9, 0.8, 0.2, 0.1}, []bool{true, true, false, false}); got != 1 {
		t.Errorf("perfect separation AUC = %.2f", got)
	}
	if got := auc([]float64{0.5, 0.5, 0.5, 0.5}, []bool{true, false, true, false}); got != 0.5 {
		t.Errorf("all-tied AUC = %.2f, want 0.5", got)
	}
}

func TestReadLabelsKeepsOnlyLabelledLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pairs.jsonl")
	data := `{"pair_id":"p1","subject_path":"a.md","session_id":"s1","label":"related"}
{"pair_id":"p2","subject_path":"a.md","session_id":"s2","label":"Unrelated"}
{"pair_id":"p3","subject_path":"a.md","session_id":"s3","label":""}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLabels(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].Related || got[1].Related {
		t.Errorf("labels = %+v", got)
	}
}
