package mediaprobe

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/bluenviron/gortmplib"
	"github.com/bluenviron/gortmplib/pkg/codecs"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
)

func openRTMP(ctx context.Context, parsedURL *url.URL) (*liveStream, error) {
	stream := newLiveStream(ctx)
	client := &gortmplib.Client{URL: cloneURL(parsedURL)}
	if err := client.Initialize(ctx); err != nil {
		stream.Close()
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, err
	}
	stream.closeFunc = client.Close

	go runRTMPReader(stream, client)
	return stream, nil
}

func runRTMPReader(stream *liveStream, client *gortmplib.Client) {
	reader := &gortmplib.Reader{Conn: client}
	if err := reader.Initialize(); err != nil {
		stream.fail(&protocolError{protocol: "RTMP", err: err})
		return
	}

	rtmpTracks := reader.Tracks()
	tracks := make([]Track, 0, len(rtmpTracks))
	for id, rtmpTrack := range rtmpTracks {
		tracks = append(tracks, rtmpTrackInfo(id, rtmpTrack))
		setRTMPCallback(reader, stream, id, rtmpTrack)
	}
	if err := stream.publishTracks(tracks); err != nil {
		return
	}

	for {
		if err := reader.Read(); err != nil {
			stream.fail(&protocolError{protocol: "RTMP", err: err})
			return
		}
	}
}

func rtmpTrackInfo(id int, track *gortmplib.Track) Track {
	info := Track{
		ID:        id,
		MediaType: MediaTypeAudio,
		Codec:     Codec(fmt.Sprintf("rtmp-%d", track.Codec.ID())),
	}
	if track.Codec.IsVideo() {
		info.MediaType = MediaTypeVideo
	}

	switch codec := track.Codec.(type) {
	case *codecs.AV1:
		info.Codec, info.ClockRate = CodecAV1, 90_000
	case *codecs.VP9:
		info.Codec, info.ClockRate = CodecVP9, 90_000
	case *codecs.H265:
		info.Codec, info.ClockRate = CodecH265, 90_000
		setH265Parameters(&info, codec.SPS)
	case *codecs.H264:
		info.Codec, info.ClockRate = CodecH264, 90_000
		setH264Parameters(&info, codec.SPS)
	case *codecs.Opus:
		info.Codec, info.ClockRate, info.SampleRate = CodecOpus, 48_000, 48_000
		info.Channels = 2
		if codec.IDHeader != nil {
			info.Channels = int(codec.IDHeader.ChannelCount)
		}
	case *codecs.FLAC:
		info.Codec = CodecFLAC
		if codec.StreamInfo != nil {
			info.ClockRate = int(codec.StreamInfo.SampleRate)
			info.SampleRate = info.ClockRate
			info.Channels = int(codec.StreamInfo.ChannelCount)
		}
	case *codecs.MPEG4Audio:
		info.Codec = CodecAAC
		if codec.Config != nil {
			info.ClockRate = codec.Config.SampleRate
			info.SampleRate = codec.Config.SampleRate
			info.Channels = mpeg4AudioChannelCount(codec.Config.ChannelConfig)
		}
	case *codecs.MPEG1Audio:
		info.Codec = CodecMPEG1Audio
	case *codecs.AC3:
		info.Codec = CodecAC3
		info.ClockRate, info.SampleRate, info.Channels = codec.SampleRate, codec.SampleRate, codec.ChannelCount
	case *codecs.G711:
		info.Codec = CodecG711
		info.ClockRate, info.SampleRate, info.Channels = 8_000, 8_000, codec.ChannelCount
	case *codecs.LPCM:
		info.Codec = CodecLPCM
		info.ClockRate, info.SampleRate, info.Channels = codec.SampleRate, codec.SampleRate, codec.ChannelCount
	}

	return info
}

func setRTMPCallback(reader *gortmplib.Reader, stream *liveStream, id int, track *gortmplib.Track) {
	publish := func(pts, dts time.Duration, size int, configure func(*Sample)) {
		sample := Sample{TrackID: id, PTS: pts, DTS: dts, Size: size}
		if configure != nil {
			configure(&sample)
		}
		stream.publishSample(sample)
	}
	publishBuffers := func(pts, dts time.Duration, buffers [][]byte, configure func(*Sample)) {
		size, ok := buffersSize(buffers)
		if !ok {
			stream.fail(ErrInvalidSample)
			return
		}
		publish(pts, dts, size, configure)
	}

	switch codec := track.Codec.(type) {
	case *codecs.AV1:
		reader.OnDataAV1(track, func(pts time.Duration, units [][]byte) {
			publishBuffers(pts, pts, units, nil)
		})
	case *codecs.VP9:
		reader.OnDataVP9(track, func(pts time.Duration, frame []byte) {
			publish(pts, pts, len(frame), nil)
		})
	case *codecs.H265:
		reader.OnDataH265(track, func(pts, dts time.Duration, units [][]byte) {
			if !h265AccessUnitHasPicture(units) {
				return
			}
			publishBuffers(pts, dts, units, nil)
		})
	case *codecs.H264:
		checker := newH264DTSChecker(90_000, codec.SPS, codec.PPS)
		reader.OnDataH264(track, func(pts, dts time.Duration, units [][]byte) {
			if !h264AccessUnitHasPicture(units) {
				return
			}
			check := checker.observe(durationToH264Ticks(pts), units)
			publishBuffers(pts, dts, units, func(sample *Sample) {
				sample.IsKeyFrame = h264.IsRandomAccess(units)
				sample.h264DTS = check
				sample.h264DTSChecked = true
			})
		})
	case *codecs.Opus:
		reader.OnDataOpus(track, func(pts time.Duration, data []byte) {
			publish(pts, pts, len(data), nil)
		})
	case *codecs.FLAC:
		reader.OnDataFLAC(track, func(pts time.Duration, data []byte) {
			publish(pts, pts, len(data), nil)
		})
	case *codecs.MPEG4Audio:
		reader.OnDataMPEG4Audio(track, func(pts time.Duration, data []byte) {
			publish(pts, pts, len(data), nil)
		})
	case *codecs.MPEG1Audio:
		reader.OnDataMPEG1Audio(track, func(pts time.Duration, data []byte) {
			publish(pts, pts, len(data), nil)
		})
	case *codecs.AC3:
		reader.OnDataAC3(track, func(pts time.Duration, data []byte) {
			publish(pts, pts, len(data), nil)
		})
	case *codecs.G711:
		reader.OnDataG711(track, func(pts time.Duration, data []byte) {
			publish(pts, pts, len(data), nil)
		})
	case *codecs.LPCM:
		reader.OnDataLPCM(track, func(pts time.Duration, data []byte) {
			publish(pts, pts, len(data), nil)
		})
	}
}

func durationToH264Ticks(value time.Duration) int64 {
	const clockRate = int64(90_000)
	return int64(value/time.Second)*clockRate +
		int64(value%time.Second)*clockRate/int64(time.Second)
}

func h264AccessUnitHasPicture(accessUnit [][]byte) bool {
	for _, nalu := range accessUnit {
		if len(nalu) == 0 {
			continue
		}
		switch h264.NALUType(nalu[0] & 0x1F) {
		case h264.NALUTypeIDR, h264.NALUTypeNonIDR:
			return true
		}
	}
	return false
}

func h265AccessUnitHasPicture(accessUnit [][]byte) bool {
	for _, nalu := range accessUnit {
		if len(nalu) != 0 && ((nalu[0]>>1)&0x3F) <= 31 {
			return true
		}
	}
	return false
}

func cloneURL(value *url.URL) *url.URL {
	cloned := *value
	return &cloned
}
