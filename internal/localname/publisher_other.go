//go:build !darwin && !windows

package localname

// systemBackend reports no system responder: Linux has no mDNS responder
// Pier can count on without extra packages, so Pier's own answers.
func systemBackend() backend { return nil }

func sweepPublishers() {}
