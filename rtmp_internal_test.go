package mediaprobe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/bluenviron/gortmplib"
	"github.com/bluenviron/gortmplib/pkg/codecs"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/flac"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/opus"
)

func TestProbeURLRTMP(t *testing.T) {
	t.Parallel()

	serverURL, serverDone := newRTMPTestServer(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	report, err := ProbeURL(
		ctx,
		serverURL,
		WithObservationDuration(60*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("ProbeURL() error = %v", err)
	}
	if !report.IsComplete || len(report.Tracks) != 1 {
		t.Fatalf("report = %+v", report)
	}
	track := report.Tracks[0]
	if track.Codec != CodecH264 || track.MediaType != MediaTypeVideo || track.ClockRate != 90_000 ||
		track.SampleCount < 3 || track.H264 == nil || track.H264.DTS.Samples < 3 {
		t.Fatalf("track report = %+v", track)
	}
	if track.H264.DTS.Error != nil {
		t.Fatalf("H264 DTS error = %+v", track.H264.DTS.Error)
	}

	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("RTMP server error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RTMP test server did not stop")
	}
}

func TestRTMPPreservesSourcePTSAndDTS(t *testing.T) {
	t.Parallel()

	serverURL, serverDone := newRTMPTestServer(t)
	parsedURL, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	stream, err := openRTMP(ctx, parsedURL)
	if err != nil {
		t.Fatalf("openRTMP() error = %v", err)
	}
	tracks, err := stream.Tracks(ctx)
	if err != nil {
		t.Fatalf("Tracks() error = %v", err)
	}
	if len(tracks) != 1 || tracks[0].Codec != CodecH264 {
		t.Fatalf("tracks = %+v", tracks)
	}
	sample, err := stream.ReadSample(ctx)
	if err != nil {
		t.Fatalf("ReadSample() error = %v", err)
	}
	if sample.PTS != 100*time.Millisecond || sample.DTS != 50*time.Millisecond ||
		!sample.IsKeyFrame || !sample.h264DTSChecked {
		t.Fatalf("sample = %+v", sample)
	}
	stream.Close()

	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("RTMP server error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RTMP test server did not stop")
	}
}

func TestProbeURLRedactsRTMPFailure(t *testing.T) {
	t.Parallel()

	const (
		username = "stream-user"
		password = "stream-password"
		secret   = "private-stream-key"
	)
	for _, scheme := range []string{"rtmp", "rtmps"} {
		t.Run(scheme, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			address := listener.Addr().String()
			if err := listener.Close(); err != nil {
				t.Fatalf("close listener: %v", err)
			}

			_, err = ProbeURL(
				t.Context(),
				scheme+"://"+username+":"+password+"@"+address+"/live#"+secret,
			)
			if err == nil {
				t.Fatal("ProbeURL() error = nil, want RTMP failure")
			}
			if errors.Is(err, ErrUnsupportedScheme) {
				t.Fatalf("%s was treated as unsupported: %v", scheme, err)
			}
			for _, sensitive := range []string{username, password, secret, address} {
				if strings.Contains(err.Error(), sensitive) {
					t.Fatalf("error contains sensitive value %q: %v", sensitive, err)
				}
			}
		})
	}
}

func TestProbeURLHonorsContextWhileWaitingForRTMP(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- connection
		}
	}()

	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, probeErr := ProbeURL(ctx, "rtmp://"+listener.Addr().String()+"/live#stream")
		result <- probeErr
	}()

	var connection net.Conn
	select {
	case connection = <-accepted:
		t.Cleanup(func() { _ = connection.Close() })
	case <-time.After(5 * time.Second):
		t.Fatal("RTMP connection did not start")
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

func TestRTMPTrackInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		codec      codecs.Codec
		mediaType  MediaType
		wantCodec  Codec
		clockRate  int
		sampleRate int
		channels   int
		width      int
		height     int
		frameRate  Rate
	}{
		{name: "AV1", codec: &codecs.AV1{}, mediaType: MediaTypeVideo, wantCodec: CodecAV1, clockRate: 90_000},
		{name: "VP9", codec: &codecs.VP9{}, mediaType: MediaTypeVideo, wantCodec: CodecVP9, clockRate: 90_000},
		{
			name: "H265", codec: &codecs.H265{SPS: testH265SPS}, mediaType: MediaTypeVideo,
			wantCodec: CodecH265, clockRate: 90_000, width: 1920, height: 1080,
			frameRate: Rate{Numerator: 30, Denominator: 1},
		},
		{
			name: "H264", codec: &codecs.H264{SPS: testH264SPS}, mediaType: MediaTypeVideo,
			wantCodec: CodecH264, clockRate: 90_000, width: 1920, height: 1080,
			frameRate: Rate{Numerator: 30, Denominator: 1},
		},
		{
			name: "Opus", codec: &codecs.Opus{IDHeader: &opus.IDHeader{ChannelCount: 6}},
			mediaType: MediaTypeAudio, wantCodec: CodecOpus, clockRate: 48_000, sampleRate: 48_000, channels: 6,
		},
		{
			name: "Opus defaults", codec: &codecs.Opus{}, mediaType: MediaTypeAudio,
			wantCodec: CodecOpus, clockRate: 48_000, sampleRate: 48_000, channels: 2,
		},
		{
			name: "FLAC", codec: &codecs.FLAC{StreamInfo: &flac.StreamInfo{SampleRate: 96_000, ChannelCount: 6}},
			mediaType: MediaTypeAudio, wantCodec: CodecFLAC, clockRate: 96_000, sampleRate: 96_000, channels: 6,
		},
		{
			name: "AAC", codec: &codecs.MPEG4Audio{Config: &mpeg4audio.AudioSpecificConfig{
				SampleRate: 48_000, ChannelConfig: 7,
			}},
			mediaType: MediaTypeAudio, wantCodec: CodecAAC, clockRate: 48_000, sampleRate: 48_000, channels: 8,
		},
		{name: "MPEG-1 audio", codec: &codecs.MPEG1Audio{}, mediaType: MediaTypeAudio, wantCodec: CodecMPEG1Audio},
		{
			name: "AC-3", codec: &codecs.AC3{SampleRate: 48_000, ChannelCount: 6},
			mediaType: MediaTypeAudio, wantCodec: CodecAC3, clockRate: 48_000, sampleRate: 48_000, channels: 6,
		},
		{
			name: "G.711", codec: &codecs.G711{ChannelCount: 1},
			mediaType: MediaTypeAudio, wantCodec: CodecG711, clockRate: 8_000, sampleRate: 8_000, channels: 1,
		},
		{
			name: "LPCM", codec: &codecs.LPCM{SampleRate: 96_000, ChannelCount: 2},
			mediaType: MediaTypeAudio, wantCodec: CodecLPCM, clockRate: 96_000, sampleRate: 96_000, channels: 2,
		},
		{name: "unknown audio", codec: testRTMPCodec{id: 123}, mediaType: MediaTypeAudio, wantCodec: Codec("rtmp-123")},
		{name: "unknown video", codec: testRTMPCodec{id: 456, video: true}, mediaType: MediaTypeVideo, wantCodec: Codec("rtmp-456")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := rtmpTrackInfo(5, &gortmplib.Track{Codec: test.codec})
			if got.ID != 5 || got.MediaType != test.mediaType || got.Codec != test.wantCodec ||
				got.ClockRate != test.clockRate || got.SampleRate != test.sampleRate ||
				got.Channels != test.channels || got.Width != test.width || got.Height != test.height ||
				got.DeclaredFrameRate != test.frameRate {
				t.Fatalf("rtmpTrackInfo() = %+v", got)
			}
		})
	}
}

func TestRTMPTimestampHelpers(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		value time.Duration
		want  int64
	}{
		{value: 1500 * time.Millisecond, want: 135_000},
		{value: -1500 * time.Millisecond, want: -135_000},
	} {
		if got := durationToH264Ticks(test.value); got != test.want {
			t.Fatalf("durationToH264Ticks(%v) = %d, want %d", test.value, got, test.want)
		}
	}
	if h264AccessUnitHasPicture([][]byte{{0x67}, {0x68}}) {
		t.Fatal("parameter sets were classified as a picture")
	}
	if !h264AccessUnitHasPicture([][]byte{{}, {0x65}}) {
		t.Fatal("IDR was not classified as a picture")
	}
	if h265AccessUnitHasPicture([][]byte{{0x40}, {0x42}, {0x44}}) {
		t.Fatal("H265 parameter sets were classified as a picture")
	}
	if !h265AccessUnitHasPicture([][]byte{{}, {0x26, 0x01}}) {
		t.Fatal("H265 IDR was not classified as a picture")
	}
}

type testRTMPCodec struct {
	id    uint32
	video bool
}

func (c testRTMPCodec) IsVideo() bool { return c.video }
func (c testRTMPCodec) ID() uint32    { return c.id }

func newRTMPTestServer(t *testing.T) (string, <-chan error) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	done := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		defer func() { _ = connection.Close() }()
		if err := connection.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
			done <- err
			return
		}

		serverConnection := &gortmplib.ServerConn{RW: connection}
		if err := serverConnection.Initialize(); err != nil {
			done <- fmt.Errorf("initialize RTMP server connection: %w", err)
			return
		}
		if err := serverConnection.Accept(); err != nil {
			done <- fmt.Errorf("accept RTMP session: %w", err)
			return
		}
		if serverConnection.Publish {
			done <- errors.New("RTMP probe connected as publisher")
			return
		}

		track := &gortmplib.Track{Codec: &codecs.H264{
			SPS: h264DTSFirstAccessUnit[0],
			PPS: h264DTSFirstAccessUnit[1],
		}}
		writer := &gortmplib.Writer{
			Conn:   serverConnection,
			Tracks: []*gortmplib.Track{track},
		}
		if err := writer.Initialize(); err != nil {
			done <- fmt.Errorf("initialize RTMP writer: %w", err)
			return
		}

		frames := [][][]byte{
			h264DTSFirstAccessUnit,
			{{0x41, 0x9a, 0x21, 0x6c, 0x45, 0xff}},
			{{0x41, 0x9a, 0x42, 0x3c, 0x21, 0x93}},
		}
		for index := 0; index < 64; index++ {
			dts := 50*time.Millisecond + time.Duration(index)*33*time.Millisecond
			pts := dts + 50*time.Millisecond
			if err := writer.WriteH264(track, pts, dts, frames[min(index, len(frames)-1)]); err != nil {
				done <- fmt.Errorf("write RTMP H264: %w", err)
				return
			}
		}

		buffer := make([]byte, 1024)
		for {
			if _, err := connection.Read(buffer); err != nil {
				done <- nil
				return
			}
		}
	}()

	return "rtmp://" + listener.Addr().String() + "/live#stream", done
}
