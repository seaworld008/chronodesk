package security

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

var ErrUnsafeCallback = errors.New("callback URL is not an allowed public HTTPS endpoint")

// ValidateHTTPSCallbackURL performs the non-network portion of callback
// validation. DNS is resolved and pinned by NewPinnedHTTPSClient immediately
// before delivery.
func ValidateHTTPSCallbackURL(target *url.URL) error {
	if target == nil ||
		!strings.EqualFold(target.Scheme, "https") ||
		target.Hostname() == "" ||
		target.User != nil {
		return ErrUnsafeCallback
	}
	host := strings.TrimSuffix(strings.ToLower(target.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return ErrUnsafeCallback
	}
	if literal := net.ParseIP(host); literal != nil && !IsPublicCallbackIP(literal) {
		return ErrUnsafeCallback
	}
	return nil
}

func ValidateHTTPSCallbackURLString(rawURL string) error {
	target, err := url.Parse(rawURL)
	if err != nil {
		return ErrUnsafeCallback
	}
	return ValidateHTTPSCallbackURL(target)
}

// NewPinnedHTTPSClient resolves the callback host once, rejects every private
// or reserved answer, and pins dialing to one validated address. Redirects and
// environment proxies are disabled so neither can change the verified target.
func NewPinnedHTTPSClient(
	ctx context.Context,
	target *url.URL,
	resolver *net.Resolver,
	timeout time.Duration,
) (*http.Client, error) {
	if err := ValidateHTTPSCallbackURL(target); err != nil {
		return nil, err
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	host := strings.TrimSuffix(strings.ToLower(target.Hostname()), ".")
	var addresses []net.IPAddr
	if literal := net.ParseIP(host); literal != nil {
		addresses = []net.IPAddr{{IP: literal}}
	} else {
		resolved, err := resolver.LookupIPAddr(ctx, host)
		if err != nil || len(resolved) == 0 {
			return nil, ErrUnsafeCallback
		}
		addresses = resolved
	}
	for _, address := range addresses {
		if !IsPublicCallbackIP(address.IP) {
			return nil, ErrUnsafeCallback
		}
	}
	port := target.Port()
	if port == "" {
		port = "443"
	}
	pinnedAddress := net.JoinHostPort(addresses[0].IP.String(), port)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(
		dialContext context.Context,
		network string,
		address string,
	) (net.Conn, error) {
		requestHost, _, err := net.SplitHostPort(address)
		if err != nil ||
			!strings.EqualFold(strings.TrimSuffix(requestHost, "."), host) {
			return nil, ErrUnsafeCallback
		}
		return (&net.Dialer{Timeout: 10 * time.Second}).
			DialContext(dialContext, network, pinnedAddress)
	}
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 20 * time.Second
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

func IsPublicCallbackIP(ip net.IP) bool {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	address = address.Unmap()
	if !address.IsGlobalUnicast() ||
		address.IsPrivate() ||
		address.IsLoopback() ||
		address.IsLinkLocalUnicast() ||
		address.IsUnspecified() {
		return false
	}
	for _, prefix := range reservedCallbackPrefixes {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

var reservedCallbackPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("2001:db8::/32"),
}
