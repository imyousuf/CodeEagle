package jev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

// Defaults for a client built without options.
const (
	// DefaultBaseURL is the service's own endpoint.
	DefaultBaseURL = "https://api.typesafe.ai/v1/systemone"

	// DefaultModel pins an exact release rather than a rolling alias.
	//
	// Gating decisions on a confidence threshold means the threshold is tuned
	// against a particular set of weights. A rolling alias can shift those
	// distributions without warning, and the failure is silent: the same
	// inputs keep returning answers, just on the other side of the line.
	DefaultModel = "jev-1.13.0"

	// DefaultTimeout bounds one attempt. Answers arrive in well under a
	// second, so this is a generous allowance for a stalled connection rather
	// than a working estimate.
	DefaultTimeout = 30 * time.Second

	// DefaultMaxRetries bounds attempts at transient failures.
	DefaultMaxRetries = 3

	// maxBackoff caps the wait between attempts, including a Retry-After the
	// service asks for: that value comes from the other end, so it is treated
	// as a request rather than an instruction.
	maxBackoff = 30 * time.Second
)

// Client talks to the Jev service.
//
// It is safe for concurrent use, and is meant to be shared: one client per
// process, not one per request.
type Client struct {
	apiKey     string
	baseURL    string
	model      string
	maxRetries int
	httpClient *http.Client
	// timeout is applied after every option has run, so that supplying a
	// client and a timeout in either order means the same thing.
	timeout time.Duration
}

// Option configures a [Client].
type Option func(*Client)

// WithBaseURL sends requests somewhere other than the service's own endpoint —
// a gateway, a proxy, or a test server.
func WithBaseURL(url string) Option {
	return func(c *Client) {
		if url != "" {
			c.baseURL = url
		}
	}
}

// WithModel pins a model version.
func WithModel(model string) Option {
	return func(c *Client) {
		if model != "" {
			c.model = model
		}
	}
}

// WithHTTPClient supplies the HTTP client, for callers that need their own
// transport, proxy or instrumentation.
//
// A timeout given by [WithTimeout] still applies, whichever order the two are
// passed in: an option that silently undid another depending on where it sat
// in the argument list would be a trap.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.httpClient = h
		}
	}
}

// WithTimeout bounds a single attempt.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithMaxRetries bounds attempts at transient failures. Zero means one attempt
// and no retry.
func WithMaxRetries(n int) Option {
	return func(c *Client) {
		if n >= 0 {
			c.maxRetries = n
		}
	}
}

// New creates a client. The API key is required.
func New(apiKey string, opts ...Option) (*Client, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("jev: no API key")
	}
	c := &Client{
		apiKey:     apiKey,
		baseURL:    DefaultBaseURL,
		model:      DefaultModel,
		maxRetries: DefaultMaxRetries,
		httpClient: &http.Client{},
	}
	for _, opt := range opts {
		opt(c)
	}
	// Applied last, so a supplied client and a supplied timeout compose
	// regardless of the order they were given in.
	switch {
	case c.timeout > 0:
		c.httpClient.Timeout = c.timeout
	case c.httpClient.Timeout == 0:
		c.httpClient.Timeout = DefaultTimeout
	}
	return c, nil
}

// Model returns the model this client asks for.
func (c *Client) Model() string { return c.model }

// request is the wire body.
type request struct {
	Model     string    `json:"model"`
	State     any       `json:"state"`
	Questions Questions `json:"questions"`
}

// Ask decides every question about one state, in a single round trip.
//
// The state may be a map, a struct, or a string; anything that marshals to
// JSON. Questions are answered in parallel and billed by input only, so asking
// everything worth knowing at once costs little more than asking one thing —
// and saves the second round trip that a follow-up question would need.
//
// Questions are validated before anything is sent, because some malformed
// ones are accepted by the service and answered meaninglessly rather than
// refused.
func (c *Client) Ask(ctx context.Context, state any, questions Questions) (*Response, error) {
	if err := questions.validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}

	body, err := json.Marshal(request{Model: c.model, State: state, Questions: questions})
	if err != nil {
		return nil, fmt.Errorf("jev: encode request: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			wait := backoff(attempt)
			var apiErr *APIError
			if errors.As(lastErr, &apiErr) {
				if after := apiErr.retryAfter; after > 0 {
					wait = min(after, maxBackoff)
				}
			}
			if err := sleep(ctx, wait); err != nil {
				return nil, err
			}
		}

		resp, err := c.attempt(ctx, body)
		if err == nil {
			return resp, nil
		}
		lastErr = err

		// A refusal the service will repeat, or a cancelled context, ends it.
		var apiErr *APIError
		if !errors.As(err, &apiErr) || !apiErr.Retryable() {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("jev: giving up after %d attempts: %w", c.maxRetries+1, lastErr)
}

// attempt performs one round trip.
func (c *Client) attempt(ctx context.Context, body []byte) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("jev: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// A connection that failed may or may not have been processed. The
		// service has no side effects, so retrying is safe.
		return nil, &APIError{StatusCode: 0, Message: err.Error(), sentinel: ErrOverloaded}
	}
	defer httpResp.Body.Close()

	raw, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("jev: read response: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		apiErr := parseAPIError(httpResp.StatusCode, raw)
		apiErr.retryAfter = parseRetryAfter(httpResp.Header.Get("Retry-After"))
		return nil, apiErr
	}

	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("jev: decode response: %w", err)
	}
	return &resp, nil
}

// backoff grows the wait between attempts, with jitter so that a batch of
// workers throttled at the same moment does not retry in lockstep.
func backoff(attempt int) time.Duration {
	base := time.Duration(1<<uint(attempt-1)) * time.Second
	return min(base, maxBackoff) + time.Duration(rand.N(int64(500*time.Millisecond)))
}

// parseRetryAfter reads a Retry-After given as whole seconds.
func parseRetryAfter(v string) time.Duration {
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

// sleep waits, or gives up early if the context is done.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
