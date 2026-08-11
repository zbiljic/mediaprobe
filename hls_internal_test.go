package mediaprobe

import (
	"math"
	"testing"
	"time"

	"github.com/bluenviron/gohlslib/v2"
	"github.com/bluenviron/gohlslib/v2/pkg/codecs"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/flac"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
)

func TestHLSTrackInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		track      *gohlslib.Track
		mediaType  MediaType
		codec      Codec
		sampleRate int
		channels   int
		width      int
		height     int
		frameRate  Rate
	}{
		{
			name:      "AV1",
			track:     &gohlslib.Track{Codec: &codecs.AV1{}, ClockRate: 90_000},
			mediaType: MediaTypeVideo,
			codec:     CodecAV1,
		},
		{
			name: "VP9",
			track: &gohlslib.Track{
				Codec: &codecs.VP9{Width: 1920, Height: 1080}, ClockRate: 90_000,
			},
			mediaType: MediaTypeVideo,
			codec:     CodecVP9,
			width:     1920,
			height:    1080,
		},
		{
			name: "H264",
			track: &gohlslib.Track{
				Codec: &codecs.H264{SPS: testH264SPS}, ClockRate: 90_000,
			},
			mediaType: MediaTypeVideo,
			codec:     CodecH264,
			width:     1920,
			height:    1080,
			frameRate: Rate{Numerator: 30, Denominator: 1},
		},
		{
			name: "H265",
			track: &gohlslib.Track{
				Codec: &codecs.H265{SPS: testH265SPS}, ClockRate: 90_000,
			},
			mediaType: MediaTypeVideo,
			codec:     CodecH265,
			width:     1920,
			height:    1080,
			frameRate: Rate{Numerator: 30, Denominator: 1},
		},
		{
			name:       "Opus",
			track:      &gohlslib.Track{Codec: &codecs.Opus{ChannelCount: 2}, ClockRate: 48_000},
			mediaType:  MediaTypeAudio,
			codec:      CodecOpus,
			sampleRate: 48_000,
			channels:   2,
		},
		{
			name: "FLAC",
			track: &gohlslib.Track{Codec: &codecs.FLAC{StreamInfo: &flac.StreamInfo{
				SampleRate: 96_000, ChannelCount: 6,
			}}},
			mediaType:  MediaTypeAudio,
			codec:      CodecFLAC,
			sampleRate: 96_000,
			channels:   6,
		},
		{
			name:      "FLAC without stream info",
			track:     &gohlslib.Track{Codec: &codecs.FLAC{}},
			mediaType: MediaTypeAudio,
			codec:     CodecFLAC,
		},
		{
			name: "MPEG-4 audio",
			track: &gohlslib.Track{Codec: &codecs.MPEG4Audio{Config: mpeg4audio.AudioSpecificConfig{
				Type: mpeg4audio.ObjectTypeAACLC, SampleRate: 48_000, ChannelConfig: 7,
			}}},
			mediaType:  MediaTypeAudio,
			codec:      CodecAAC,
			sampleRate: 48_000,
			channels:   8,
		},
		{
			name:      "KLV",
			track:     &gohlslib.Track{Codec: &codecs.KLV{}, ClockRate: 90_000},
			mediaType: MediaTypeData,
			codec:     CodecKLV,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := hlsTrackInfo(3, test.track)
			if got.ID != 3 || got.ClockRate != test.track.ClockRate ||
				got.MediaType != test.mediaType || got.Codec != test.codec ||
				got.SampleRate != test.sampleRate || got.Channels != test.channels ||
				got.Width != test.width || got.Height != test.height ||
				got.DeclaredFrameRate != test.frameRate {
				t.Fatalf("hlsTrackInfo() = %+v", got)
			}
		})
	}
}

func TestMediaHelpers(t *testing.T) {
	t.Parallel()

	t.Run("ticks", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			value     int64
			clockRate int
			want      time.Duration
		}{
			{value: 4_500, clockRate: 90_000, want: 50 * time.Millisecond},
			{value: -4_500, clockRate: 90_000, want: -50 * time.Millisecond},
			{value: 1, clockRate: 0, want: 0},
			{value: math.MaxInt64, clockRate: 1, want: time.Duration(math.MaxInt64)},
			{value: math.MinInt64, clockRate: 1, want: time.Duration(math.MinInt64)},
		}
		for _, test := range tests {
			if got := ticksToDuration(test.value, test.clockRate); got != test.want {
				t.Fatalf("ticksToDuration(%d, %d) = %v, want %v", test.value, test.clockRate, got, test.want)
			}
		}
	})

	t.Run("rate", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			numerator   uint64
			denominator uint64
			want        Rate
		}{
			{numerator: 25_000, denominator: 1_000, want: Rate{Numerator: 25, Denominator: 1}},
			{numerator: 30_000, denominator: 1_001, want: Rate{Numerator: 30_000, Denominator: 1_001}},
			{numerator: 0, denominator: 1},
			{numerator: 1, denominator: 0},
			{numerator: math.MaxUint64, denominator: 1},
		}
		for _, test := range tests {
			if got := rationalRate(test.numerator, test.denominator); got != test.want {
				t.Fatalf(
					"rationalRate(%d, %d) = %v, want %v",
					test.numerator,
					test.denominator,
					got,
					test.want,
				)
			}
		}
	})

	t.Run("buffers", func(t *testing.T) {
		t.Parallel()

		size, ok := buffersSize([][]byte{{1, 2}, {3, 4, 5}})
		if !ok || size != 5 {
			t.Fatalf("buffersSize() = %d, %v, want 5, true", size, ok)
		}
	})
}

var testH264SPS = []byte{
	0x67, 0x42, 0xc0, 0x28, 0xd9, 0x00, 0x78, 0x02,
	0x27, 0xe5, 0x84, 0x00, 0x00, 0x03, 0x00, 0x04,
	0x00, 0x00, 0x03, 0x00, 0xf0, 0x3c, 0x60, 0xc9,
	0x20,
}

var testH265SPS = []byte{
	0x42, 0x01, 0x01, 0x01, 0x60, 0x00, 0x00, 0x03,
	0x00, 0x90, 0x00, 0x00, 0x03, 0x00, 0x00, 0x03,
	0x00, 0x78, 0xa0, 0x03, 0xc0, 0x80, 0x10, 0xe5,
	0x96, 0x66, 0x69, 0x24, 0xca, 0xe0, 0x10, 0x00,
	0x00, 0x03, 0x00, 0x10, 0x00, 0x00, 0x03, 0x01,
	0xe0, 0x80,
}
