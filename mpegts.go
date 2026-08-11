package mediaprobe

import (
	"io"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts/codecs"
)

func runMPEGTSReader(stream *liveStream, connection io.Reader, protocol string) {
	reader := &mpegts.Reader{R: connection}
	if err := reader.Initialize(); err != nil {
		stream.fail(&protocolError{protocol: protocol, err: err})
		return
	}

	mpegTracks := reader.Tracks()
	tracks := make([]Track, 0, len(mpegTracks))
	for id, mpegTrack := range mpegTracks {
		tracks = append(tracks, mpegTSTrackInfo(id, mpegTrack))
		setMPEGTSCallback(reader, stream, id, mpegTrack)
	}
	if err := stream.publishTracks(tracks); err != nil {
		return
	}

	for {
		if err := reader.Read(); err != nil {
			stream.fail(&protocolError{protocol: protocol, err: err})
			return
		}
	}
}

func mpegTSTrackInfo(id int, track *mpegts.Track) Track {
	const clockRate = 90_000
	info := Track{ID: id, ClockRate: clockRate}
	switch codec := track.Codec.(type) {
	case *codecs.H265:
		info.MediaType, info.Codec = MediaTypeVideo, CodecH265
	case *codecs.H264:
		info.MediaType, info.Codec = MediaTypeVideo, CodecH264
	case *codecs.MPEG4Video:
		info.MediaType, info.Codec = MediaTypeVideo, CodecMPEG4Video
	case *codecs.MPEG1Video:
		info.MediaType, info.Codec = MediaTypeVideo, CodecMPEG1Video
	case *codecs.Opus:
		info.MediaType, info.Codec, info.SampleRate = MediaTypeAudio, CodecOpus, 48_000
		if codec.Desc != nil {
			info.Channels = codec.Desc.ChannelCount()
		}
	case *codecs.MPEG4Audio, *codecs.MPEG4AudioLATM:
		info.MediaType, info.Codec = MediaTypeAudio, CodecAAC
		if typed, ok := track.Codec.(*codecs.MPEG4Audio); ok {
			info.SampleRate = typed.SampleRate
			info.Channels = mpeg4AudioChannelCount(typed.ChannelConfig)
		}
	case *codecs.MPEG1Audio:
		info.MediaType, info.Codec = MediaTypeAudio, CodecMPEG1Audio
	case *codecs.AC3:
		info.MediaType, info.Codec = MediaTypeAudio, CodecAC3
		info.SampleRate, info.Channels = codec.SampleRate, codec.ChannelCount
	case *codecs.EAC3:
		info.MediaType, info.Codec = MediaTypeAudio, CodecEAC3
		info.SampleRate, info.Channels = codec.SampleRate, codec.ChannelCount
	case *codecs.KLV:
		info.MediaType, info.Codec = MediaTypeData, CodecKLV
	case *codecs.DVBSubtitle:
		info.MediaType, info.Codec = MediaTypeData, Codec("dvb-subtitle")
	default:
		info.MediaType = MediaTypeData
		info.Codec = Codec("mpegts-unsupported")
	}

	return info
}

func setMPEGTSCallback(reader *mpegts.Reader, stream *liveStream, id int, track *mpegts.Track) {
	publish := func(pts, dts int64, size int, configure func(*Sample)) error {
		sample := Sample{
			TrackID: id,
			PTS:     ticksToDuration(pts, 90_000),
			DTS:     ticksToDuration(dts, 90_000),
			Size:    size,
		}
		if configure != nil {
			configure(&sample)
		}
		stream.publishSample(sample)
		return nil
	}
	publishBuffers := func(pts, dts int64, buffers [][]byte, configure func(*Sample)) error {
		size, ok := buffersSize(buffers)
		if !ok {
			return ErrInvalidSample
		}
		return publish(pts, dts, size, configure)
	}

	switch track.Codec.(type) {
	case *codecs.H265:
		reader.OnDataH265(track, func(pts, dts int64, units [][]byte) error {
			return publishBuffers(pts, dts, units, nil)
		})
	case *codecs.H264:
		checker := newH264DTSChecker(90_000, nil, nil)
		reader.OnDataH264(track, func(pts, dts int64, units [][]byte) error {
			check := checker.observe(pts, units)
			return publishBuffers(pts, dts, units, func(sample *Sample) {
				sample.IsKeyFrame = h264.IsRandomAccess(units)
				sample.h264DTS = check
				sample.h264DTSChecked = true
			})
		})
	case *codecs.MPEG4Video, *codecs.MPEG1Video:
		reader.OnDataMPEGxVideo(track, func(pts int64, frame []byte) error {
			return publish(pts, pts, len(frame), nil)
		})
	case *codecs.Opus:
		reader.OnDataOpus(track, func(pts int64, packets [][]byte) error {
			return publishBuffers(pts, pts, packets, nil)
		})
	case *codecs.MPEG4Audio:
		reader.OnDataMPEG4Audio(track, func(pts int64, units [][]byte) error {
			return publishBuffers(pts, pts, units, nil)
		})
	case *codecs.MPEG4AudioLATM:
		reader.OnDataMPEG4AudioLATM(track, func(pts int64, units [][]byte) error {
			return publishBuffers(pts, pts, units, nil)
		})
	case *codecs.MPEG1Audio:
		reader.OnDataMPEG1Audio(track, func(pts int64, frames [][]byte) error {
			return publishBuffers(pts, pts, frames, nil)
		})
	case *codecs.AC3:
		reader.OnDataAC3(track, func(pts int64, frame []byte) error {
			return publish(pts, pts, len(frame), nil)
		})
	case *codecs.EAC3:
		reader.OnDataEAC3(track, func(pts int64, frame []byte) error {
			return publish(pts, pts, len(frame), nil)
		})
	case *codecs.KLV:
		reader.OnDataKLV(track, func(pts int64, data []byte) error {
			return publish(pts, pts, len(data), nil)
		})
	case *codecs.DVBSubtitle:
		reader.OnDataDVBSubtitle(track, func(pts int64, data []byte) error {
			return publish(pts, pts, len(data), nil)
		})
	}
}
