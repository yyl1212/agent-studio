package builtin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

var (
	ErrInvalidHTTPURL        = errors.New("invalid HTTP URL")
	ErrPrivateAddressBlocked = errors.New("private network address blocked")
	ErrTooManyRedirects      = errors.New("too many HTTP redirects")
	ErrCrossOriginRedirect   = errors.New("cross-origin HTTP redirect blocked")
)

type lookupIPFunc func(context.Context, string, string) ([]net.IP, error)

func (node *httpNode) validateURL(ctx context.Context, rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return nil, fmt.Errorf("%w: malformed URL", ErrInvalidHTTPURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("%w: unsupported scheme", ErrInvalidHTTPURL)
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("%w: userinfo is not allowed", ErrInvalidHTTPURL)
	}
	if node.allowPrivateNetwork {
		return parsed, nil
	}
	addresses, err := node.resolveHost(ctx, parsed.Hostname())
	if err != nil {
		return nil, err
	}
	for _, address := range addresses {
		if isRestrictedAddress(address) {
			return nil, fmt.Errorf("%w: %s", ErrPrivateAddressBlocked, parsed.Hostname())
		}
	}
	return parsed, nil
}

func (node *httpNode) resolveHost(ctx context.Context, host string) ([]netip.Addr, error) {
	if address, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{address.Unmap()}, nil
	}
	resolved, err := node.lookupIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve HTTP host: %w", err)
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("resolve HTTP host: no addresses")
	}
	addresses := make([]netip.Addr, 0, len(resolved))
	for _, ip := range resolved {
		address, ok := netip.AddrFromSlice(ip)
		if !ok {
			return nil, fmt.Errorf("resolve HTTP host: invalid address")
		}
		addresses = append(addresses, address.Unmap())
	}
	return addresses, nil
}

func isRestrictedAddress(address netip.Addr) bool {
	return address.IsLoopback() ||
		address.IsPrivate() ||
		address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() ||
		address.IsMulticast() ||
		address.IsUnspecified()
}

func (node *httpNode) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("split HTTP dial address: %w", err)
	}
	addresses, err := node.resolveHost(ctx, host)
	if err != nil {
		return nil, err
	}
	if !node.allowPrivateNetwork {
		for _, resolved := range addresses {
			if isRestrictedAddress(resolved) {
				return nil, fmt.Errorf("%w: %s", ErrPrivateAddressBlocked, host)
			}
		}
	}
	dialer := &net.Dialer{}
	var lastErr error
	for _, resolved := range addresses {
		connection, err := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.String(), port))
		if err == nil {
			return connection, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("dial HTTP host: %w", lastErr)
}

func (node *httpNode) newClient() *http.Client {
	var client http.Client
	if node.client != nil {
		client = *node.client
	} else {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = http.ProxyFromEnvironment
		transport.DialContext = node.dialContext
		client.Transport = transport
	}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return ErrTooManyRedirects
		}
		if _, err := node.validateURL(request.Context(), request.URL.String()); err != nil {
			return err
		}
		origin := via[0].URL
		if !strings.EqualFold(request.URL.Scheme, origin.Scheme) || !strings.EqualFold(request.URL.Host, origin.Host) {
			return ErrCrossOriginRedirect
		}
		return nil
	}
	return &client
}

func sensitiveHeader(name string) bool {
	normalized := normalizeCredentialKey(name)
	for _, marker := range []string{"authorization", "cookie", "token", "secret", "password", "apikey", "credential", "subscriptionkey"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	for _, part := range splitCredentialKey(name) {
		if part == "auth" {
			return true
		}
	}
	return false
}

func sensitiveJSONKey(name string) bool {
	normalized := normalizeCredentialKey(name)
	for _, marker := range []string{"authorization", "cookie", "token", "secret", "password", "apikey", "credential", "subscriptionkey"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return normalized == "auth"
}

func normalizeCredentialKey(name string) string {
	return strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(name)))
}

func splitCredentialKey(name string) []string {
	return strings.FieldsFunc(strings.ToLower(strings.TrimSpace(name)), func(character rune) bool {
		return character == '-' || character == '_' || character == ' '
	})
}
