# Retry Runbook

What to do when retries pile up in production.

## Symptoms

Request latency climbs to a minute and the error rate stays flat, because every
call is retried with backoff before it fails.

## Remedy

Shed load at the edge until the upstream service recovers.
