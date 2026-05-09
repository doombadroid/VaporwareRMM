// Package httputil hosts HTTP helpers shared across the server. The
// SafeOutboundClient is the SSRF chokepoint for any user-supplied URL the
// server itself fetches: webhooks, AI provider endpoints, integration
// probes. Centralised here so a future hardening (extra address ranges to
// block, redirect policy, response size cap) lands in one place.
package httputil

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"
)

// SafeOutboundClient returns an http.Client whose Transport refuses to dial
// loopback / private / link-local destinations and refuses to follow
// redirects (a redirect to http://169.254.169.254 would otherwise let an
// attacker bypass the dial-time check by registering a public hostname that
// 302s into the cloud-metadata range).
//
// The dial-time check uses net.Dialer.Control on the resolved IP, which
// makes DNS rebinding ineffective: every connect re-validates against the
// kernel's just-resolved address.
func SafeOutboundClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: timeout,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("dial: address %s is not an IP", address)
			}
			if isBlockedAddr(ip) {
				return fmt.Errorf("dial: refusing to connect to non-public address %s", ip.String())
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:     dialer.DialContext,
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
		// Don't follow redirects. A redirect chain like
		//   POST https://attacker.example/webhook
		//      -> 307 http://169.254.169.254/latest/meta-data/iam/...
		// would otherwise reach metadata services even though the original
		// URL passed RejectPrivateHost.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// RejectPrivateHost validates an URL's scheme + initial-resolution hosts.
// This catches obviously-bad URLs early at write time (so the operator gets
// a 400 when registering a webhook to localhost); the dial-time Control
// function in SafeOutboundClient is the real defense at fetch time.
func RejectPrivateHost(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("url scheme must be http or https")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("url missing host")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("dns lookup failed: %w", err)
	}
	for _, ip := range ips {
		if isBlockedAddr(ip) {
			return fmt.Errorf("url resolves to a non-public address (%s)", ip.String())
		}
	}
	return nil
}

// MetadataSafeClient is a less restrictive variant for callers that may
// legitimately reach private addresses (operator-provided AI providers
// pointing at an internal Ollama on 192.168.x.x), but must never reach
// cloud-metadata services. Disables redirects so an Authorization header
// sent to api.openai.com can't be replayed against 169.254.169.254 via
// a 307.
func MetadataSafeClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: timeout,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return nil
			}
			if isMetadataAddr(ip) {
				return fmt.Errorf("dial: refusing to connect to cloud-metadata address %s", ip.String())
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:     dialer.DialContext,
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func isMetadataAddr(ip net.IP) bool {
	// AWS / GCP / Azure all use 169.254.169.254 (link-local).
	if ip.IsLinkLocalUnicast() {
		return true
	}
	// Alibaba: 100.100.100.200 (CGNAT range used as metadata).
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1] == 100 && ip4[2] == 100 && ip4[3] == 200 {
		return true
	}
	return false
}

func isBlockedAddr(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	// Cloud-metadata addresses (AWS/GCP/Azure all share 169.254.169.254;
	// Alibaba uses 100.100.100.200) are caught by IsLinkLocalUnicast above
	// for AWS, but Alibaba's CGNAT range (100.64.0.0/10) is not flagged by
	// IsPrivate. Block it explicitly so a webhook to Alibaba metadata can't
	// pivot through CGNAT.
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] < 128 {
		return true
	}
	return false
}
