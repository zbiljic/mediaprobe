package mediaprobe

import (
	"context"
	"io"
	"net"
	"net/url"
	"strings"
)

func mpegTSProtocol(scheme string) string {
	return strings.ToUpper(scheme) + " MPEG-TS"
}

func openMPEGTSURL(ctx context.Context, parsedURL *url.URL) (*liveStream, error) {
	scheme := strings.ToLower(parsedURL.Scheme)
	var (
		reader io.ReadCloser
		err    error
	)
	switch scheme {
	case "tcp":
		reader, err = (&net.Dialer{}).DialContext(ctx, "tcp", parsedURL.Host)
	case "udp":
		reader, err = listenUDP(parsedURL)
	default:
		return nil, ErrUnsupportedScheme
	}
	if err != nil {
		return nil, err
	}

	stream := newLiveStream(ctx)
	stream.closeFunc = func() { _ = reader.Close() }
	go runMPEGTSReader(stream, reader, mpegTSProtocol(scheme))
	return stream, nil
}

const maximumUDPDatagramSize = 64 * 1024

type udpStreamReader struct {
	connection *net.UDPConn
	source     net.IP
	datagram   []byte
	pending    []byte
}

func listenUDP(parsedURL *url.URL) (*udpStreamReader, error) {
	address, err := net.ResolveUDPAddr("udp", parsedURL.Host)
	if err != nil {
		return nil, err
	}

	var connection *net.UDPConn
	if address.IP.IsMulticast() {
		connection, err = net.ListenMulticastUDP("udp", nil, address)
	} else {
		connection, err = net.ListenUDP("udp", address)
	}
	if err != nil {
		return nil, err
	}

	var source net.IP
	if rawSource := parsedURL.Query().Get("source"); rawSource != "" {
		source = net.ParseIP(rawSource)
		if source == nil {
			_ = connection.Close()
			return nil, ErrInvalidURL
		}
	}

	return &udpStreamReader{
		connection: connection,
		source:     source,
		datagram:   make([]byte, maximumUDPDatagramSize),
	}, nil
}

func (r *udpStreamReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if len(r.pending) != 0 {
		read := copy(buffer, r.pending)
		r.pending = r.pending[read:]
		return read, nil
	}

	for {
		read, address, err := r.connection.ReadFromUDP(r.datagram)
		if err != nil {
			return 0, err
		}
		if r.source != nil && !address.IP.Equal(r.source) {
			continue
		}
		if read == 0 {
			continue
		}

		r.pending = r.datagram[:read]
		copied := copy(buffer, r.pending)
		r.pending = r.pending[copied:]
		return copied, nil
	}
}

func (r *udpStreamReader) Close() error {
	return r.connection.Close()
}
