package fieldcalls

import (
	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
)

// Linker demonstrates chained field method calls.
type Linker struct {
	store    graph.Store
	settings *config.Settings
}

func (l *Linker) Run() {
	l.store.QueryNodes("type", "Function")
	l.settings.Validate()
}

func (l *Linker) helper() {
	l.store.AddNode(nil)
}
