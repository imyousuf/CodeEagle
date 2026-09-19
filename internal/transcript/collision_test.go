package transcript

import (
	"testing"
)

func stat(label string, firstAt, lastAt float64) SpeakerEvidence {
	return SpeakerEvidence{Stat: SpeakerStat{Label: label, FirstAt: firstAt, LastAt: lastAt}}
}

// TestResolveCollisions covers two labels given the same name.
//
// One person's label sometimes changes partway through a recording, and both
// halves should resolve to them. But two labels that overlap in time are two
// people, and giving them one name puts one person's words in the other's
// mouth — the evidence for a name is weakest in exactly that case, so the
// speaker stays unresolved instead.
func TestResolveCollisions(t *testing.T) {
	tests := []struct {
		name       string
		identities []SpeakerIdentity
		evidence   []SpeakerEvidence
		wantNames  map[string]string
	}{
		{
			name: "overlapping labels with one name lose it",
			identities: []SpeakerIdentity{
				{Label: "Person 1", Name: "Mona", Confidence: 0.9, Method: MethodJudge},
				{Label: "Person 3", Name: "Mona", Confidence: 0.8, Method: MethodJudge},
			},
			// They take turns with each other, so they are two people.
			evidence:  []SpeakerEvidence{stat("Person 1", 10, 900), stat("Person 3", 30, 880)},
			wantNames: map[string]string{"Person 1": "Mona", "Person 3": ""},
		},
		{
			name: "an exact tie unresolves both",
			identities: []SpeakerIdentity{
				{Label: "Person 1", Name: "Mona", Confidence: 0.8, Method: MethodJudge},
				{Label: "Person 3", Name: "Mona", Confidence: 0.8, Method: MethodJudge},
			},
			evidence:  []SpeakerEvidence{stat("Person 1", 10, 900), stat("Person 3", 30, 880)},
			wantNames: map[string]string{"Person 1": "", "Person 3": ""},
		},
		{
			name: "labels that never overlap are the drift case and both keep the name",
			identities: []SpeakerIdentity{
				{Label: "Person 1", Name: "Kevin", Confidence: 0.9, Method: MethodJudge},
				{Label: "Person 4", Name: "Kevin", Confidence: 0.7, Method: MethodJudge},
			},
			// One stops before the other starts: the diarizer lost the thread.
			evidence:  []SpeakerEvidence{stat("Person 1", 10, 400), stat("Person 4", 450, 900)},
			wantNames: map[string]string{"Person 1": "Kevin", "Person 4": "Kevin"},
		},
		{
			name: "different names are left alone",
			identities: []SpeakerIdentity{
				{Label: "Person 1", Name: "Kevin", Confidence: 0.9, Method: MethodJudge},
				{Label: "Person 3", Name: "Mona", Confidence: 0.9, Method: MethodJudge},
			},
			evidence:  []SpeakerEvidence{stat("Person 1", 10, 900), stat("Person 3", 30, 880)},
			wantNames: map[string]string{"Person 1": "Kevin", "Person 3": "Mona"},
		},
		{
			name: "the host is never unresolved by a collision",
			identities: []SpeakerIdentity{
				{Label: "You", Name: "Imran Yousuf", Confidence: 1, Method: MethodOwnerAnchor},
				{Label: "Person 1", Name: "Imran Yousuf", Confidence: 0.9, Method: MethodJudge},
			},
			evidence:  []SpeakerEvidence{stat("You", 0, 900), stat("Person 1", 30, 880)},
			wantNames: map[string]string{"You": "Imran Yousuf", "Person 1": "Imran Yousuf"},
		},
		{
			name: "a spelling variant of one name still collides",
			identities: []SpeakerIdentity{
				{Label: "Person 1", Name: "Mona", Confidence: 0.9, Method: MethodJudge},
				{Label: "Person 3", Name: "mona", Confidence: 0.6, Method: MethodJudge},
			},
			evidence:  []SpeakerEvidence{stat("Person 1", 10, 900), stat("Person 3", 30, 880)},
			wantNames: map[string]string{"Person 1": "Mona", "Person 3": ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveCollisions(tt.identities, tt.evidence)

			byLabel := make(map[string]SpeakerIdentity, len(got))
			for _, id := range got {
				byLabel[id.Label] = id
			}
			for label, want := range tt.wantNames {
				id, ok := byLabel[label]
				if !ok {
					t.Fatalf("%s was dropped entirely", label)
				}
				if id.Name != want {
					t.Errorf("%s = %q, want %q", label, id.Name, want)
				}
				if id.Name == "" && id.Confidence != 0 {
					t.Errorf("%s kept confidence %.2f after losing its name", label, id.Confidence)
				}
			}
		})
	}
}

// TestResolveCollisionsRecordsWhy covers leaving a trace, so an unresolved
// speaker can be told from one nobody ever had a name for.
func TestResolveCollisionsRecordsWhy(t *testing.T) {
	got := resolveCollisions(
		[]SpeakerIdentity{
			{Label: "Person 1", Name: "Mona", Confidence: 0.9, Method: MethodJudge},
			{Label: "Person 3", Name: "Mona", Confidence: 0.8, Method: MethodJudge},
		},
		[]SpeakerEvidence{stat("Person 1", 10, 900), stat("Person 3", 30, 880)},
	)

	for _, id := range got {
		if id.Label != "Person 3" {
			continue
		}
		if id.Evidence == "" {
			t.Error("the demoted speaker records no reason")
		}
		return
	}
	t.Fatal("Person 3 was dropped entirely")
}
