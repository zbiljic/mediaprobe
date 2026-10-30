package mediaprobe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts/codecs"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts/substructs"
	srt "github.com/datarhei/gosrt"
)

func TestProbeURLSRT(t *testing.T) {
	t.Parallel()

	serverURL, serverDone := newSRTTestServer(t, videoMPEGTSWithDTS(t))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	report, err := ProbeURL(ctx, serverURL, WithObservationDuration(60*time.Millisecond))
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

	waitForSRTServer(t, serverDone)
}

func TestSRTPreservesMPEGTSPTSAndDTS(t *testing.T) {
	t.Parallel()

	serverURL, serverDone := newSRTTestServer(t, videoMPEGTSWithDTS(t))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	stream, err := openSRT(ctx, serverURL)
	if err != nil {
		t.Fatalf("openSRT() error = %v", err)
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

	waitForSRTServer(t, serverDone)
}

func TestProbeURLRedactsSRTFailure(t *testing.T) {
	t.Parallel()

	config := srt.DefaultConfig()
	listener, err := srt.Listen("srt", "127.0.0.1:0", config)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(listener.Close)

	rejected := make(chan error, 1)
	go func() {
		request, acceptErr := listener.Accept2()
		if acceptErr != nil {
			rejected <- acceptErr
			return
		}
		request.Reject(srt.REJ_BADSECRET)
		rejected <- nil
	}()

	const (
		passphrase = "private-passphrase"
		streamID   = "private-stream-id"
	)
	address := listener.Addr().String()
	_, err = ProbeURL(t.Context(), "srt://"+address+"?passphrase="+passphrase+"&streamid="+streamID)
	if err == nil {
		t.Fatal("ProbeURL() error = nil, want SRT failure")
	}
	if errors.Is(err, ErrUnsupportedScheme) {
		t.Fatalf("SRT was treated as unsupported: %v", err)
	}
	for _, sensitive := range []string{passphrase, streamID, address} {
		if strings.Contains(err.Error(), sensitive) {
			t.Fatalf("error contains sensitive value %q: %v", sensitive, err)
		}
	}

	select {
	case rejectErr := <-rejected:
		if rejectErr != nil {
			t.Fatalf("reject SRT connection: %v", rejectErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SRT test server did not reject connection")
	}
}

func TestProbeURLHonorsContextWhileWaitingForSRT(t *testing.T) {
	t.Parallel()

	packetConnection, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	t.Cleanup(func() { _ = packetConnection.Close() })

	handshakeStarted := make(chan struct{})
	go func() {
		buffer := make([]byte, 2048)
		if _, _, readErr := packetConnection.ReadFrom(buffer); readErr == nil {
			close(handshakeStarted)
		}
	}()

	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, probeErr := ProbeURL(ctx, "srt://"+packetConnection.LocalAddr().String()+"?conntimeo=250")
		result <- probeErr
	}()

	select {
	case <-handshakeStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("SRT handshake did not start")
	}
	cancel()

	select {
	case probeErr := <-result:
		if !errors.Is(probeErr, context.Canceled) {
			t.Fatalf("ProbeURL() error = %v, want context.Canceled", probeErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ProbeURL did not stop after context cancellation")
	}
}

func TestMPEGTSTrackInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		codec      codecs.Codec
		mediaType  MediaType
		wantCodec  Codec
		sampleRate int
		channels   int
	}{
		{name: "H265", codec: &codecs.H265{}, mediaType: MediaTypeVideo, wantCodec: CodecH265},
		{name: "H264", codec: &codecs.H264{}, mediaType: MediaTypeVideo, wantCodec: CodecH264},
		{name: "MPEG-4 video", codec: &codecs.MPEG4Video{}, mediaType: MediaTypeVideo, wantCodec: CodecMPEG4Video},
		{name: "MPEG-1 video", codec: &codecs.MPEG1Video{}, mediaType: MediaTypeVideo, wantCodec: CodecMPEG1Video},
		{
			name: "Opus", codec: &codecs.Opus{Desc: &substructs.OpusAudioDescriptor{ChannelConfigCode: 0x06}},
			mediaType: MediaTypeAudio, wantCodec: CodecOpus, sampleRate: 48_000, channels: 6,
		},
		{
			name: "AAC", codec: &codecs.MPEG4Audio{Config: mpeg4audio.AudioSpecificConfig{SampleRate: 48_000, ChannelConfig: 7}},
			mediaType: MediaTypeAudio, wantCodec: CodecAAC, sampleRate: 48_000, channels: 8,
		},
		{name: "AAC LATM", codec: &codecs.MPEG4AudioLATM{}, mediaType: MediaTypeAudio, wantCodec: CodecAAC},
		{name: "MPEG-1 audio", codec: &codecs.MPEG1Audio{}, mediaType: MediaTypeAudio, wantCodec: CodecMPEG1Audio},
		{
			name: "AC-3", codec: &codecs.AC3{SampleRate: 48_000, ChannelCount: 6},
			mediaType: MediaTypeAudio, wantCodec: CodecAC3, sampleRate: 48_000, channels: 6,
		},
		{
			name: "E-AC-3", codec: &codecs.EAC3{SampleRate: 48_000, ChannelCount: 8},
			mediaType: MediaTypeAudio, wantCodec: CodecEAC3, sampleRate: 48_000, channels: 8,
		},
		{name: "KLV", codec: &codecs.KLV{}, mediaType: MediaTypeData, wantCodec: CodecKLV},
		{name: "DVB subtitle", codec: &codecs.DVBSubtitle{}, mediaType: MediaTypeData, wantCodec: Codec("dvb-subtitle")},
		{name: "unsupported", codec: &codecs.Unsupported{}, mediaType: MediaTypeData, wantCodec: Codec("mpegts-unsupported")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := mpegTSTrackInfo(5, &mpegts.Track{Codec: test.codec})
			if got.ID != 5 || got.MediaType != test.mediaType || got.Codec != test.wantCodec ||
				got.ClockRate != 90_000 || got.SampleRate != test.sampleRate || got.Channels != test.channels {
				t.Fatalf("mpegTSTrackInfo() = %+v", got)
			}
		})
	}
}

func TestMPEGTSMPEGVideoSamples(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		codec     codecs.Codec
		wantCodec Codec
		write     func(*mpegts.Writer, *mpegts.Track, int64, []byte) error
	}{
		{
			name: "MPEG-4 video", codec: &codecs.MPEG4Video{}, wantCodec: CodecMPEG4Video,
			write: (*mpegts.Writer).WriteMPEG4Video,
		},
		{
			name: "MPEG-1/2 video", codec: &codecs.MPEG1Video{}, wantCodec: CodecMPEG1Video,
			write: (*mpegts.Writer).WriteMPEG1Video,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var output bytes.Buffer
			track := &mpegts.Track{Codec: test.codec}
			writer := &mpegts.Writer{W: &output, Tracks: []*mpegts.Track{track}}
			if err := writer.Initialize(); err != nil {
				t.Fatalf("initialize MPEG-TS writer: %v", err)
			}
			frame := []byte{0, 0, 1, 0xb8, 1, 2, 3, 4}
			if err := test.write(writer, track, 9_000, frame); err != nil {
				t.Fatalf("write MPEG-TS video: %v", err)
			}

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			stream := newLiveStream(ctx)
			t.Cleanup(stream.Close)
			go runMPEGTSReader(stream, bytes.NewReader(output.Bytes()), "MPEG-TS")

			tracks, err := stream.Tracks(ctx)
			if err != nil {
				t.Fatalf("Tracks() error = %v", err)
			}
			if len(tracks) != 1 || tracks[0].Codec != test.wantCodec {
				t.Fatalf("tracks = %+v", tracks)
			}
			sample, err := stream.ReadSample(ctx)
			if err != nil {
				t.Fatalf("ReadSample() error = %v", err)
			}
			if sample.TrackID != tracks[0].ID || sample.PTS != 100*time.Millisecond ||
				sample.DTS != sample.PTS || sample.Size != len(frame) {
				t.Fatalf("sample = %+v", sample)
			}
		})
	}
}

func newSRTTestServer(t *testing.T, payload []byte) (string, <-chan error) {
	t.Helper()

	config := srt.DefaultConfig()
	listener, err := srt.Listen("srt", "127.0.0.1:0", config)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(listener.Close)

	done := make(chan error, 1)
	go func() {
		request, acceptErr := listener.Accept2()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		connection, acceptErr := request.Accept()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		defer func() { _ = connection.Close() }()

		if _, copyErr := io.Copy(connection, bytes.NewReader(payload)); copyErr != nil {
			done <- fmt.Errorf("write SRT payload: %w", copyErr)
			return
		}

		buffer := make([]byte, 1024)
		for {
			if _, readErr := connection.Read(buffer); readErr != nil {
				done <- nil
				return
			}
		}
	}()

	return "srt://" + listener.Addr().String(), done
}

func waitForSRTServer(t *testing.T, done <-chan error) {
	t.Helper()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SRT server error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SRT test server did not stop")
	}
}

func videoMPEGTSWithDTS(t *testing.T) []byte {
	t.Helper()

	var output bytes.Buffer
	track := &mpegts.Track{Codec: &codecs.H264{}}
	writer := &mpegts.Writer{W: &output, Tracks: []*mpegts.Track{track}}
	if err := writer.Initialize(); err != nil {
		t.Fatalf("initialize MPEG-TS writer: %v", err)
	}

	frames := [][][]byte{
		h264DTSFirstAccessUnit,
		{{0x41, 0x9a, 0x21, 0x6c, 0x45, 0xff}},
		{{0x41, 0x9a, 0x42, 0x3c, 0x21, 0x93}},
		{{0x41, 0x9a, 0x63, 0x49, 0xe1, 0x0f}},
	}
	for index := 0; index < 64; index++ {
		dts := int64(4_500 + index*3_000)
		pts := dts + 4_500
		if err := writer.WriteH264(track, pts, dts, frames[min(index, len(frames)-1)]); err != nil {
			t.Fatalf("write MPEG-TS H264: %v", err)
		}
	}

	return output.Bytes()
}
