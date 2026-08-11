package mediaprobe

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"
)

type httpOptions struct {
	client  *http.Client
	headers map[string]string
}

// WithHTTPClient sets the client used for HTTP-based streams. ProbeURL does
// not mutate the client. A nil client uses default HTTP settings.
func WithHTTPClient(client *http.Client) Option {
	return func(options *probeOptions) {
		options.http.client = client
	}
}

// WithHTTPHeaders adds headers to every request made for HTTP-based streams,
// including cross-origin resources referenced by a playlist. The values are
// copied when this option is created and whenever it is applied.
func WithHTTPHeaders(headers map[string]string) Option {
	copied := maps.Clone(headers)
	return func(options *probeOptions) {
		options.http.headers = maps.Clone(copied)
	}
}

// ProbeURL connects to a live media URL and observes it for the configured
// duration of media time. HTTP(S) HLS and RTSP(S) URLs are currently
// supported. Callers should provide a context with an appropriate wall-clock
// deadline.
func ProbeURL(ctx context.Context, rawURL string, options ...Option) (Report, error) {
	config := applyOptions(options)
	if err := validateOptions(config); err != nil {
		return Report{}, err
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return Report{}, ErrInvalidURL
	}

	scheme := strings.ToLower(parsed.Scheme)
	var stream *liveStream
	var protocol string
	switch scheme {
	case "http", "https":
		protocol = "HLS"
		stream, err = openHLS(ctx, rawURL, config)
	case "rtsp", "rtsps":
		protocol = "RTSP"
		stream, err = openRTSP(ctx, rawURL)
	default:
		return Report{}, fmt.Errorf("%w: %s", ErrUnsupportedScheme, scheme)
	}

	if err != nil {
		return Report{}, &protocolError{protocol: protocol, err: err}
	}
	defer stream.Close()

	return probeStream(ctx, stream, config)
}

// protocolError deliberately omits the wrapped error text because protocol
// errors can contain credentials, hosts, or signed query parameters. Unwrap
// retains errors.Is and errors.As behavior for callers that need the
// underlying cause.
type protocolError struct {
	protocol string
	err      error
}

func (e *protocolError) Error() string {
	return "mediaprobe: " + e.protocol + " client error"
}

func (e *protocolError) Unwrap() error {
	return e.err
}
