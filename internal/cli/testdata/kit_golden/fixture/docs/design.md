# Retry Design

Status: Draft

The client retries a failed call with exponential backoff, so a brief outage
does not surface as an error to the caller.

## Backoff

Each attempt waits twice as long as the one before, starting at half a second
and never longer than thirty seconds. The calculation lives in
[backoff.py](../scripts/backoff.py).

## Limits

A call is attempted at most five times. After the fifth failure the error is
returned unchanged, and the store keeps no record of it.

## Related

See the [README](../README.md) and the [operator notes](../notes.txt).
