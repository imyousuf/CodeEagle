package relate

import (
	"context"
	"encoding/json"
	"sync"
)

// Ledger is what relating remembers between runs: every verdict, kept with
// the content hashes it was given under, and for each document the content
// under which its pairs were last all considered. A pair is asked about
// again only when either document's hash changes, and a document is taken
// up again only when its own does.
//
// Rejected verdicts are kept too: they are what stops a pair being asked
// about on every run.
//
// MemLedger keeps one in memory for a program that saves it whole.
// CodeEagle keeps each verdict on the relation of the pair's lower document
// and the rest on its scan-state node. Methods are called from one goroutine
// at a time.
type Ledger interface {
	// Verdict returns the verdict kept on the pair a, b (a the lower ID),
	// or nil.
	Verdict(a, b string) *Verdict
	// Put keeps a verdict on the pair a, b, replacing any before it.
	Put(a, b Doc, v *Verdict)
	// Considered returns the hash under which doc's pairs were last all
	// considered, or "" if they never were.
	Considered(doc string) string
	// Consider records that doc's pairs were all considered under hash.
	Consider(doc, hash string)
	// Save stores what was put and considered since the last save.
	Save(ctx context.Context) error
}

// Judged reports whether the ledger holds a verdict on the pair as both
// documents are now.
func Judged(l Ledger, c *Candidate) bool {
	return l.Verdict(c.A.ID, c.B.ID).Current(c.A, c.B)
}

// MemLedger is a Ledger held in memory. It encodes to JSON and decodes from
// it with encoding/json, and Save hands that JSON to the function it was
// made with, so a program stores it wherever it keeps its index.
type MemLedger struct {
	mu         sync.Mutex
	verdicts   map[string]map[string]*Verdict // A -> B -> verdict
	considered map[string]string
	save       func(ctx context.Context, data []byte) error
}

// NewMemLedger returns an empty ledger. save may be nil, making Save do
// nothing; to start from a saved ledger, json.Unmarshal into the result.
func NewMemLedger(save func(ctx context.Context, data []byte) error) *MemLedger {
	return &MemLedger{
		verdicts:   make(map[string]map[string]*Verdict),
		considered: make(map[string]string),
		save:       save,
	}
}

// Verdict returns the verdict kept on the pair a, b, or nil.
func (l *MemLedger) Verdict(a, b string) *Verdict {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.verdicts[a][b]
}

// Put keeps a verdict on the pair a, b.
func (l *MemLedger) Put(a, b Doc, v *Verdict) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.verdicts[a.ID] == nil {
		l.verdicts[a.ID] = make(map[string]*Verdict)
	}
	l.verdicts[a.ID][b.ID] = v
}

// Considered returns the hash under which doc's pairs were last all
// considered.
func (l *MemLedger) Considered(doc string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.considered[doc]
}

// Consider records that doc's pairs were all considered under hash.
func (l *MemLedger) Consider(doc, hash string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.considered[doc] = hash
}

// Forget drops a document that has left the corpus: whether it was
// considered, and every verdict on a pair it is part of.
func (l *MemLedger) Forget(doc string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.considered, doc)
	delete(l.verdicts, doc)
	for a, byB := range l.verdicts {
		delete(byB, doc)
		if len(byB) == 0 {
			delete(l.verdicts, a)
		}
	}
}

// Save hands the ledger's JSON to the save function, if there is one.
func (l *MemLedger) Save(ctx context.Context) error {
	if l.save == nil {
		return nil
	}
	data, err := json.Marshal(l)
	if err != nil {
		return err
	}
	return l.save(ctx, data)
}

// memLedgerJSON is how a MemLedger is written: verdicts by the lower
// document, then by the other.
type memLedgerJSON struct {
	Verdicts   map[string]map[string]*Verdict `json:"verdicts"`
	Considered map[string]string              `json:"considered"`
}

// MarshalJSON encodes the ledger.
func (l *MemLedger) MarshalJSON() ([]byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return json.Marshal(memLedgerJSON{Verdicts: l.verdicts, Considered: l.considered})
}

// UnmarshalJSON replaces the ledger's contents with a saved ledger's,
// keeping its save function.
func (l *MemLedger) UnmarshalJSON(data []byte) error {
	var in memLedgerJSON
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	if in.Verdicts == nil {
		in.Verdicts = make(map[string]map[string]*Verdict)
	}
	if in.Considered == nil {
		in.Considered = make(map[string]string)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.verdicts, l.considered = in.Verdicts, in.Considered
	return nil
}
