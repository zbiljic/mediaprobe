package mediaprobe

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/bluenviron/gohlslib/v2"
	"github.com/bluenviron/gohlslib/v2/pkg/codecs"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
)

func openHLS(ctx context.Context, rawURL string, config probeOptions) (*liveStream, error) {
	stream := newLiveStream(ctx)
	// gohlslib's default download callbacks log complete URLs, which can contain
	// credentials or signed query parameters.
	client := &gohlslib.Client{
		URI:                       rawURL,
		HTTPClient:                config.http.client,
		OnDownloadPrimaryPlaylist: func(string) {},
		OnDownloadStreamPlaylist:  func(string) {},
		OnDownloadSegment:         func(string) {},
		OnDownloadPart:            func(string) {},
		OnDecodeError:             func(error) {},
		OnRequest: func(request *http.Request) {
			for name, value := range config.http.headers {
				request.Header.Set(name, value)
			}
		},
	}
	client.OnTracks = func(hlsTracks []*gohlslib.Track) error {
		tracks := make([]Track, 0, len(hlsTracks))
		for index, hlsTrack := range hlsTracks {
			tracks = append(tracks, hlsTrackInfo(index, hlsTrack))
			setHLSCallback(client, stream, index, hlsTrack)
		}

		return stream.publishTracks(tracks)
	}

	if err := client.Start(); err != nil {
		stream.Close()
		return nil, err
	}
	stream.closeFunc = client.Close
	go func() {
		err := client.Wait2()
		if errors.Is(err, gohlslib.ErrClientEOS) {
			stream.fail(io.EOF)
			return
		}
		if err != nil {
			stream.fail(&protocolError{protocol: "HLS", err: err})
		}
	}()

	return stream, nil
}

func hlsTrackInfo(id int, track *gohlslib.Track) Track {
	info := Track{ID: id, ClockRate: track.ClockRate}
	switch codec := track.Codec.(type) {
	case *codecs.AV1:
		info.MediaType, info.Codec = MediaTypeVideo, CodecAV1
	case *codecs.VP9:
		info.MediaType, info.Codec = MediaTypeVideo, CodecVP9
		info.Width, info.Height = codec.Width, codec.Height
	case *codecs.H265:
		info.MediaType, info.Codec = MediaTypeVideo, CodecH265
		setH265Parameters(&info, codec.SPS)
	case *codecs.H264:
		info.MediaType, info.Codec = MediaTypeVideo, CodecH264
		setH264Parameters(&info, codec.SPS)
	case *codecs.Opus:
		info.MediaType, info.Codec = MediaTypeAudio, CodecOpus
		info.SampleRate, info.Channels = track.ClockRate, codec.ChannelCount
	case *codecs.FLAC:
		info.MediaType, info.Codec = MediaTypeAudio, CodecFLAC
		if codec.StreamInfo != nil {
			info.SampleRate = int(codec.StreamInfo.SampleRate)
			info.Channels = int(codec.StreamInfo.ChannelCount)
		}
	case *codecs.MPEG4Audio:
		info.MediaType, info.Codec = MediaTypeAudio, CodecAAC
		info.SampleRate = codec.Config.SampleRate
		info.Channels = mpeg4AudioChannelCount(codec.Config.ChannelConfig)
	case *codecs.KLV:
		info.MediaType, info.Codec = MediaTypeData, CodecKLV
	}

	return info
}

func setHLSCallback(client *gohlslib.Client, stream *liveStream, id int, track *gohlslib.Track) {
	publish := func(pts, dts int64, size int, configure func(*Sample)) {
		sample := Sample{
			TrackID: id,
			PTS:     ticksToDuration(pts, track.ClockRate),
			DTS:     ticksToDuration(dts, track.ClockRate),
			Size:    size,
		}
		if configure != nil {
			configure(&sample)
		}
		stream.publishSample(sample)
	}
	publishBuffers := func(pts, dts int64, buffers [][]byte, configure func(*Sample)) {
		size, ok := buffersSize(buffers)
		if !ok {
			stream.fail(ErrInvalidSample)
			return
		}
		publish(pts, dts, size, configure)
	}

	switch codec := track.Codec.(type) {
	case *codecs.AV1:
		client.OnDataAV1(track, func(pts int64, units [][]byte) {
			publishBuffers(pts, pts, units, nil)
		})
	case *codecs.VP9:
		client.OnDataVP9(track, func(pts int64, frame []byte) {
			publish(pts, pts, len(frame), nil)
		})
	case *codecs.H265:
		client.OnDataH26x(track, func(pts, dts int64, units [][]byte) {
			publishBuffers(pts, dts, units, nil)
		})
	case *codecs.H264:
		checker := newH264DTSChecker(track.ClockRate, codec.SPS, codec.PPS)
		client.OnDataH26x(track, func(pts, dts int64, units [][]byte) {
			check := checker.observe(pts, units)
			publishBuffers(pts, dts, units, func(sample *Sample) {
				sample.h264DTS = check
				sample.h264DTSChecked = true
			})
		})
	case *codecs.Opus:
		client.OnDataOpus(track, func(pts int64, packets [][]byte) {
			publishBuffers(pts, pts, packets, nil)
		})
	case *codecs.FLAC:
		client.OnDataFLAC(track, func(pts int64, frame []byte) {
			publish(pts, pts, len(frame), nil)
		})
	case *codecs.MPEG4Audio:
		client.OnDataMPEG4Audio(track, func(pts int64, units [][]byte) {
			publishBuffers(pts, pts, units, nil)
		})
	case *codecs.KLV:
		client.OnDataKLV(track, func(pts int64, unit []byte) {
			publish(pts, pts, len(unit), nil)
		})
	}
}

func setH264Parameters(track *Track, rawSPS []byte) {
	var sps h264.SPS
	if sps.Unmarshal(rawSPS) != nil {
		return
	}
	track.Width, track.Height = sps.Width(), sps.Height()
	if sps.VUI != nil && sps.VUI.TimingInfo != nil {
		track.DeclaredFrameRate = rationalRate(
			uint64(sps.VUI.TimingInfo.TimeScale),
			2*uint64(sps.VUI.TimingInfo.NumUnitsInTick),
		)
	}
}

func setH265Parameters(track *Track, rawSPS []byte) {
	var sps h265.SPS
	if sps.Unmarshal(rawSPS) != nil {
		return
	}
	track.Width, track.Height = sps.Width(), sps.Height()
	if sps.VUI != nil && sps.VUI.TimingInfo != nil {
		track.DeclaredFrameRate = rationalRate(
			uint64(sps.VUI.TimingInfo.TimeScale),
			uint64(sps.VUI.TimingInfo.NumUnitsInTick),
		)
	}
}
