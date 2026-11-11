# ADR 001: Evict cache entries by age

Status: Accepted
Date: 2026-01-10

## Context

The store held every entry forever, and memory grew without bound under a
steady stream of distinct keys.

## Decision

Entries older than one hour are evicted on the next write. Eviction by age was
chosen over least-recently-used because it needs no bookkeeping on reads.

## Consequences

A key read constantly is still evicted after an hour, and the next read misses.
