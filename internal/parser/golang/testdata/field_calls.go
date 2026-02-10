package fieldcalls

import (
	"github.com/imyousuf/CodeEagle/internal/config"
	"github.com/imyousuf/CodeEagle/internal/graph"
)

// Logger is a local type with methods, used to test same-package field type resolution.
type Logger struct{}

func (lg *Logger) Info(msg string)  {}
func (lg *Logger) Error(msg string) {}

// Inner is used to test deeper chains: Linker -> Inner -> nested field.
type Inner struct {
	logger Logger
}

// Linker demonstrates chained field method calls.
type Linker struct {
	store    graph.Store
	settings *config.Settings
	logger   Logger
	inner    Inner
}

func (l *Linker) Run() {
	l.store.QueryNodes("type", "Function")
	l.settings.Validate()
	// Local field type: should resolve directly to Logger.Info method node.
	l.logger.Info("starting")
}

func (l *Linker) helper() {
	l.store.AddNode(nil)
	l.logger.Error("oops")
}

// DeepChain tests three-level chain: l.inner.logger.Info().
func (l *Linker) DeepChain() {
	l.inner.logger.Info("deep")
}
