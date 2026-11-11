package vectorstore

import (
	"context"
	"hash/fnv"
	"math"
	"math/rand"
)

// randomEmbedder gives every distinct text its own direction, seeded by the
// text, so the index has the connectivity a real embedding space has. The
// hash-based mock bunches texts together and the walk stalls after a few
// hops, which says nothing about ef.
type randomEmbedder struct{ dims int }

func (r *randomEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		h := fnv.New64a()
		h.Write([]byte(text))
		rng := rand.New(rand.NewSource(int64(h.Sum64())))
		vec := make([]float32, r.dims)
		var norm float64
		for j := range vec {
			vec[j] = float32(rng.NormFloat64())
			norm += float64(vec[j] * vec[j])
		}
		for j := range vec {
			vec[j] /= float32(math.Sqrt(norm))
		}
		out[i] = vec
	}
	return out, nil
}

func (r *randomEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	v, err := r.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	return v[0], nil
}
func (r *randomEmbedder) Dimensions() int   { return r.dims }
func (r *randomEmbedder) Name() string      { return "random" }
func (r *randomEmbedder) ModelName() string { return "random-embed" }
