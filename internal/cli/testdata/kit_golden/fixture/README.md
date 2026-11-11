# Kit Golden Fixture

A small project used to pin what CodeEagle indexes and how it answers.

## Layout

- [Retry design](docs/design.md) explains how the client retries failed calls.
- [Cache decision](docs/adr-001-cache.md) records why entries are evicted.
- The key-value store lives in [store.go](src/store.go).
- The backoff calculator is [backoff.py](scripts/backoff.py).

## Running

Nothing here runs; the files exist to be read by the indexer.
