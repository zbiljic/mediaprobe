package mediaprobe_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts"
	mpegtscodecs "github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts/codecs"
	"github.com/zbiljic/mediaprobe"
)

func TestProbeURLHLS(t *testing.T) {
	t.Parallel()

	headers := map[string]string{"X-Stream-Token": "private-token"}
	headerOption := mediaprobe.WithHTTPHeaders(headers)
	headers["X-Stream-Token"] = "changed-after-option-creation"

	transport := &headerTransport{
		base:  http.DefaultTransport,
		name:  "X-Custom-Client",
		value: "used",
	}
	httpClient := &http.Client{Transport: transport}
	server := newHLSTestServer(t, map[string]string{
		"X-Stream-Token":  "private-token",
		"X-Custom-Client": "used",
	})

	report, err := mediaprobe.ProbeURL(
		t.Context(),
		server.URL+"/index.m3u8",
		mediaprobe.WithObservationDuration(100*time.Millisecond),
		headerOption,
		mediaprobe.WithHTTPClient(httpClient),
	)
	requireNoError(t, err)
	requireEqual(t, report.IsComplete, true)
	requireEqual(t, len(report.Tracks), 1)
	requireEqual(t, report.Tracks[0].Codec, mediaprobe.CodecAAC)
	requireEqual(t, report.Tracks[0].MediaType, mediaprobe.MediaTypeAudio)
	requireEqual(t, report.Tracks[0].SampleRate, 48_000)
	requireEqual(t, report.Tracks[0].Channels, 2)
	requireEqual(t, report.Tracks[0].SampleCount >= 2, true)
	if httpClient.Transport != transport {
		t.Fatal("ProbeURL mutated the supplied HTTP client")
	}
}

func TestProbeURLHLSChecksH264DTS(t *testing.T) {
	t.Parallel()

	server := newHLSVideoTestServer(t)
	report, err := mediaprobe.ProbeURL(
		t.Context(),
		server.URL+"/index.m3u8",
		mediaprobe.WithObservationDuration(60*time.Millisecond),
	)
	requireNoError(t, err)
	requireEqual(t, len(report.Tracks), 1)
	requireEqual(t, report.Tracks[0].Codec, mediaprobe.CodecH264)
	if report.Tracks[0].H264 == nil {
		t.Fatal("H264 report is nil")
	}
	requireEqual(t, report.Tracks[0].H264.DTS.Samples >= 2, true)
	if report.Tracks[0].H264.DTS.Error != nil {
		t.Fatalf("H264 DTS error = %+v", report.Tracks[0].H264.DTS.Error)
	}
}

func TestProbeURLReturnsIncompleteHLSReport(t *testing.T) {
	t.Parallel()

	server := newHLSTestServer(t, nil)
	report, err := mediaprobe.ProbeURL(
		t.Context(),
		server.URL+"/index.m3u8",
		mediaprobe.WithObservationDuration(200*time.Millisecond),
	)
	requireErrorIs(t, err, mediaprobe.ErrObservationIncomplete)
	requireEqual(t, report.IsComplete, false)
	requireEqual(t, len(report.Tracks), 1)
	requireEqual(t, report.Tracks[0].SampleCount >= 2, true)
}

func TestProbeURLRejectsInvalidAndUnsupportedURLs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rawURL   string
		expected error
	}{
		{name: "empty", rawURL: "", expected: mediaprobe.ErrInvalidURL},
		{name: "relative", rawURL: "stream.m3u8", expected: mediaprobe.ErrInvalidURL},
		{name: "malformed", rawURL: "https://example.com/%zz", expected: mediaprobe.ErrInvalidURL},
		{
			name:     "unsupported",
			rawURL:   "ftp://stream-user:stream-password@example.com/live",
			expected: mediaprobe.ErrUnsupportedScheme,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := mediaprobe.ProbeURL(t.Context(), test.rawURL)
			requireErrorIs(t, err, test.expected)
			for _, sensitive := range []string{"stream-user", "stream-password"} {
				if strings.Contains(err.Error(), sensitive) {
					t.Fatalf("error contains sensitive value %q: %v", sensitive, err)
				}
			}
		})
	}
}

func TestProbeURLRedactsHLSFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		http.Error(writer, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	const secret = "private-signed-token"
	_, err := mediaprobe.ProbeURL(t.Context(), server.URL+"/index.m3u8?token="+secret)
	if err == nil {
		t.Fatal("ProbeURL() error = nil, want HLS failure")
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), server.URL) {
		t.Fatalf("error exposes URL details: %v", err)
	}
}

func TestProbeURLHonorsContextWhileWaitingForHLS(t *testing.T) {
	t.Parallel()

	requestStarted := make(chan struct{})
	var startedOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		startedOnce.Do(func() { close(requestStarted) })
		<-request.Context().Done()
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := mediaprobe.ProbeURL(ctx, server.URL+"/index.m3u8")
		result <- err
	}()

	select {
	case <-requestStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("HLS request did not start")
	}
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ProbeURL() error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ProbeURL did not stop after context cancellation")
	}
}

func TestProbeURLValidatesOptionsBeforeConnecting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		option   mediaprobe.Option
		expected error
	}{
		{
			name:     "observation duration",
			option:   mediaprobe.WithObservationDuration(0),
			expected: mediaprobe.ErrInvalidObservationDuration,
		},
		{
			name:     "sample limit",
			option:   mediaprobe.WithSampleLimit(0),
			expected: mediaprobe.ErrInvalidSampleLimit,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := mediaprobe.ProbeURL(
				t.Context(),
				"https://user:password@example.com/live.m3u8",
				test.option,
			)
			requireErrorIs(t, err, test.expected)
			if strings.Contains(err.Error(), "password") {
				t.Fatalf("error contains URL password: %v", err)
			}
		})
	}
}

func newHLSTestServer(t *testing.T, requiredHeaders map[string]string) *httptest.Server {
	t.Helper()

	segment := audioMPEGTS(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		for name, expected := range requiredHeaders {
			if request.Header.Get(name) != expected {
				http.Error(writer, "missing required header", http.StatusUnauthorized)
				return
			}
		}

		switch request.URL.Path {
		case "/index.m3u8":
			writer.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = writer.Write([]byte("#EXTM3U\n" +
				"#EXT-X-VERSION:3\n" +
				"#EXT-X-TARGETDURATION:1\n" +
				"#EXT-X-PLAYLIST-TYPE:VOD\n" +
				"#EXTINF:1,\n" +
				"segment.ts\n" +
				"#EXT-X-ENDLIST\n"))
		case "/segment.ts":
			writer.Header().Set("Content-Type", "video/MP2T")
			_, _ = writer.Write(segment)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func newHLSVideoTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	segment := videoMPEGTS(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/index.m3u8":
			writer.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = writer.Write([]byte("#EXTM3U\n" +
				"#EXT-X-VERSION:3\n" +
				"#EXT-X-TARGETDURATION:1\n" +
				"#EXT-X-PLAYLIST-TYPE:VOD\n" +
				"#EXTINF:1,\n" +
				"segment.ts\n" +
				"#EXT-X-ENDLIST\n"))
		case "/segment.ts":
			writer.Header().Set("Content-Type", "video/MP2T")
			_, _ = writer.Write(segment)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func audioMPEGTS(t *testing.T) []byte {
	t.Helper()

	var output bytes.Buffer
	track := &mpegts.Track{Codec: &mpegtscodecs.MPEG4Audio{Config: mpeg4audio.AudioSpecificConfig{
		Type:          mpeg4audio.ObjectTypeAACLC,
		SampleRate:    48_000,
		ChannelConfig: 2,
	}}}
	writer := &mpegts.Writer{W: &output, Tracks: []*mpegts.Track{track}}
	requireNoError(t, writer.Initialize())
	for timestamp := int64(0); timestamp <= 9_000; timestamp += 4_500 {
		requireNoError(t, writer.WriteMPEG4Audio(track, timestamp, [][]byte{{1, 2, 3, 4}}))
	}

	return output.Bytes()
}

func videoMPEGTS(t *testing.T) []byte {
	t.Helper()

	var output bytes.Buffer
	track := &mpegts.Track{Codec: &mpegtscodecs.H264{}}
	writer := &mpegts.Writer{W: &output, Tracks: []*mpegts.Track{track}}
	requireNoError(t, writer.Initialize())
	requireNoError(t, writer.WriteH264(track, 0, 0, h264FirstAccessUnit))
	requireNoError(t, writer.WriteH264(track, 3_000, 3_000, [][]byte{{
		0x41, 0x9a, 0x21, 0x6c, 0x45, 0xff,
	}}))
	requireNoError(t, writer.WriteH264(track, 6_000, 6_000, [][]byte{{
		0x41, 0x9a, 0x42, 0x3c, 0x21, 0x93,
	}}))

	return output.Bytes()
}

var h264FirstAccessUnit = [][]byte{
	{
		0x67, 0x64, 0x00, 0x28, 0xac, 0xd9, 0x40, 0x78,
		0x02, 0x27, 0xe5, 0x84, 0x00, 0x00, 0x03, 0x00,
		0x04, 0x00, 0x00, 0x03, 0x00, 0xf0, 0x3c, 0x60,
		0xc6, 0x58,
	},
	{0x68, 0xce, 0x3c, 0x80},
	{0x65, 0x88, 0x84, 0x00, 0x33, 0xff},
}

type headerTransport struct {
	base  http.RoundTripper
	name  string
	value string
}

func (t *headerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header = request.Header.Clone()
	cloned.Header.Set(t.name, t.value)
	return t.base.RoundTrip(cloned)
}
