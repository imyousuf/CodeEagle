package graph

import (
	"context"
	"io"
)

// Exporter can serialize all graph data (nodes and edges) to a writer.
type Exporter interface {
	Export(ctx context.Context, w io.Writer) error
}

// Importer can deserialize graph data from a reader, replacing all existing data.
type Importer interface {
	Import(ctx context.Context, r io.Reader) error
}
