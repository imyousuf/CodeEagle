// Package health reports whether an upstream service is reachable.
package health

// Healthy reports whether the last probe succeeded.
func Healthy(lastProbeOK bool) bool {
	return lastProbeOK
}
