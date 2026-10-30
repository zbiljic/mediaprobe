package mediaprobe

import (
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/pion/rtp/v2"
)

func TestRTSPTrackInfo(t *testing.T) {
	t.Parallel()

	generic := &format.Generic{PayloadTyp: 123, RTPMa: "custom/1000"}
	if err := generic.Init(); err != nil {
		t.Fatalf("initialize generic format: %v", err)
	}
	tests := []struct {
		name       string
		mediaType  description.MediaType
		format     format.Format
		wantType   MediaType
		codec      Codec
		sampleRate int
		channels   int
		width      int
		height     int
		frameRate  Rate
	}{
		{name: "AV1", mediaType: description.MediaTypeVideo, format: &format.AV1{}, wantType: MediaTypeVideo, codec: CodecAV1},
		{name: "VP9", mediaType: description.MediaTypeVideo, format: &format.VP9{}, wantType: MediaTypeVideo, codec: CodecVP9},
		{name: "VP8", mediaType: description.MediaTypeVideo, format: &format.VP8{}, wantType: MediaTypeVideo, codec: CodecVP8},
		{
			name: "H265", mediaType: description.MediaTypeVideo,
			format: &format.H265{SPS: testH265SPS}, wantType: MediaTypeVideo, codec: CodecH265,
			width: 1920, height: 1080, frameRate: Rate{Numerator: 30, Denominator: 1},
		},
		{
			name: "H264", mediaType: description.MediaTypeVideo,
			format: &format.H264{SPS: testH264SPS}, wantType: MediaTypeVideo, codec: CodecH264,
			width: 1920, height: 1080, frameRate: Rate{Numerator: 30, Denominator: 1},
		},
		{name: "MPEG-4 video", mediaType: description.MediaTypeVideo, format: &format.MPEG4Video{}, wantType: MediaTypeVideo, codec: CodecMPEG4Video},
		{name: "MPEG-1 video", mediaType: description.MediaTypeVideo, format: &format.MPEG1Video{}, wantType: MediaTypeVideo, codec: CodecMPEG1Video},
		{name: "MJPEG", mediaType: description.MediaTypeVideo, format: &format.MJPEG{}, wantType: MediaTypeVideo, codec: CodecJPEG},
		{
			name: "Opus", mediaType: description.MediaTypeAudio,
			format: &format.Opus{ChannelCount: 6}, wantType: MediaTypeAudio, codec: CodecOpus,
			sampleRate: 48_000, channels: 6,
		},
		{
			name: "MPEG-4 audio", mediaType: description.MediaTypeAudio,
			format: &format.MPEG4Audio{Config: &mpeg4audio.AudioSpecificConfig{
				SampleRate: 48_000, ChannelConfig: 7,
			}},
			wantType: MediaTypeAudio, codec: CodecAAC, sampleRate: 48_000, channels: 8,
		},
		{name: "MPEG-1 audio", mediaType: description.MediaTypeAudio, format: &format.MPEG1Audio{}, wantType: MediaTypeAudio, codec: CodecMPEG1Audio},
		{
			name: "AC-3", mediaType: description.MediaTypeAudio,
			format:   &format.AC3{SampleRate: 48_000, ChannelCount: 6},
			wantType: MediaTypeAudio, codec: CodecAC3, sampleRate: 48_000, channels: 6,
		},
		{
			name: "G.711", mediaType: description.MediaTypeAudio,
			format:   &format.G711{SampleRate: 8_000, ChannelCount: 1},
			wantType: MediaTypeAudio, codec: CodecG711, sampleRate: 8_000, channels: 1,
		},
		{
			name: "LPCM", mediaType: description.MediaTypeAudio,
			format:   &format.LPCM{SampleRate: 96_000, ChannelCount: 2},
			wantType: MediaTypeAudio, codec: CodecLPCM, sampleRate: 96_000, channels: 2,
		},
		{name: "KLV", mediaType: description.MediaTypeApplication, format: &format.KLV{}, wantType: MediaTypeData, codec: CodecKLV},
		{name: "generic", mediaType: description.MediaTypeApplication, format: generic, wantType: MediaTypeData, codec: Codec("rtp-123")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := rtspTrackInfo(4, test.mediaType, test.format)
			if got.ID != 4 || got.ClockRate != test.format.ClockRate() ||
				got.MediaType != test.wantType || got.Codec != test.codec ||
				got.SampleRate != test.sampleRate || got.Channels != test.channels ||
				got.Width != test.width || got.Height != test.height ||
				got.DeclaredFrameRate != test.frameRate {
				t.Fatalf("rtspTrackInfo() = %+v", got)
			}
		})
	}
}

func TestRTSPH264ReportsDTSFailure(t *testing.T) {
	t.Parallel()

	mediaFormat := &format.H264{PayloadTyp: 96, PacketizationMode: 1}
	stream := newLiveStream(t.Context())
	aggregator, err := newRTPAggregator(stream, nil, map[format.Format]int{mediaFormat: 7})
	if err != nil {
		t.Fatalf("newRTPAggregator() error = %v", err)
	}

	sample, ok := aggregator.handleH264(
		aggregator.states[mediaFormat].h264,
		mediaFormat,
		&rtp.Packet{
			Header:  rtp.Header{Version: 2, Marker: true, PayloadType: 96, Timestamp: 90_000},
			Payload: []byte{0x65},
		},
		90_000,
	)
	if !ok {
		t.Fatal("handleH264() did not publish a sample")
	}
	if sample.TrackID != 7 || sample.PTS != time.Second || sample.DTS != sample.PTS ||
		!sample.IsKeyFrame || !sample.h264DTSChecked {
		t.Fatalf("sample = %+v", sample)
	}
	if sample.h264DTS.Error == nil || sample.h264DTS.Error.Code != H264DTSErrorMissingSPS {
		t.Fatalf("H264 DTS report = %+v", sample.h264DTS)
	}
}
