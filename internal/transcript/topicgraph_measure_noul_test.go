package transcript

import (
	"regexp"
	"testing"
)

// TestTopicMeasureAGI reports where the worked example's labels sit in each
// other's nearest lists, which says whether embeddings alone would have
// proposed them, and what the tree already says about them.
//
// Measured 2026-09-20: the three AGI labels sit at cosine 0.60-0.64 from
// each other while each one's fifteen nearest labels sit at 0.70-0.78, so
// none of the three pairs is proposed by embedding; one is proposed by a
// shared meeting, one by a shared parent, and one by nothing.
func TestTopicMeasureAGI(t *testing.T) {
	profiles, closeAll := measureCorpus(t)
	defer closeAll()
	agi := regexp.MustCompile(`(?i)\bAGI\b|superintelligence|recursive self`)
	all := profiles.All()
	for _, p := range all {
		if !agi.MatchString(p.Node.Name) {
			continue
		}
		var parents []string
		for _, id := range p.Parents {
			if pp := profiles.Get(id); pp != nil {
				parents = append(parents, pp.Node.Name)
			} else {
				parents = append(parents, id)
			}
		}
		t.Logf("%s (meetings %d, parents %v)", p.Node.Name, len(p.Meetings), parents)
		for _, n := range profiles.nearest(p, all, 15) {
			mark := ""
			if agi.MatchString(n.profile.Node.Name) {
				mark = "  <== AGI"
			}
			t.Logf("   %2d  %.2f  %s%s", n.rank, n.cosine, n.profile.Node.Name, mark)
		}
	}
}
