"""Exponential backoff for retried calls."""

MAX_ATTEMPTS = 5


def compute_backoff(attempt, base=0.5, cap=30.0):
    """Return how many seconds to wait before the given attempt."""
    return min(cap, base * (2 ** attempt))


class RetryPolicy:
    """Decides whether a failed call is tried again."""

    def __init__(self, max_attempts=MAX_ATTEMPTS):
        self.max_attempts = max_attempts

    def should_retry(self, attempt):
        """Return True while attempts remain."""
        return attempt < self.max_attempts

    def delay(self, attempt):
        return compute_backoff(attempt)
