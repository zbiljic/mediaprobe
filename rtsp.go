package mediaprobe

import (
	"context"
	"fmt"
	"sync"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/pion/rtp"
)

func openRTSP(ctx context.Context, rawURL string) (*liveStream, error) {
	parsedURL, err := base.ParseURL(rawURL)
	if err != nil {
		return nil, ErrInvalidURL
	}

	stream := newLiveStream(ctx)
	client := &gortsplib.Client{
		Scheme:            parsedURL.Scheme,
		Host:              parsedURL.Host,
		OnTransportSwitch: func(error) {},
		OnPacketsLost:     func(uint64) {},
		OnDecodeError:     func(error) {},
	}
	if err := client.Start(); err != nil {
		stream.Close()
		return nil, err
	}

	var closeOnce sync.Once
	closeClient := func() {
		closeOnce.Do(client.Close)
	}
	stream.closeFunc = closeClient
	stopContextClose := context.AfterFunc(ctx, closeClient)
	defer stopContextClose()

	closeWithError := func(err error) (*liveStream, error) {
		stream.Close()
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, err
	}

	sessionDescription, _, err := client.Describe(parsedURL)
	if err != nil {
		return closeWithError(err)
	}

	medias := make([]*description.Media, 0, len(sessionDescription.Medias))
	for _, media := range sessionDescription.Medias {
		if !media.IsBackChannel {
			medias = append(medias, media)
		}
	}
	if err := client.SetupAll(sessionDescription.BaseURL, medias); err != nil {
		return closeWithError(err)
	}

	tracks, trackIDs := rtspTracks(medias)
	aggregator, err := newRTPAggregator(stream, client, trackIDs)
	if err != nil {
		return closeWithError(err)
	}
	client.OnPacketRTPAny(aggregator.handle)

	if _, err := client.Play(nil); err != nil {
		return closeWithError(err)
	}
	if err := stream.publishTracks(tracks); err != nil {
		return closeWithError(err)
	}

	go func() {
		if err := client.Wait(); err != nil {
			stream.fail(&protocolError{protocol: "RTSP", err: err})
		}
	}()

	return stream, nil
}

func rtspTracks(medias []*description.Media) ([]Track, map[format.Format]int) {
	var tracks []Track
	trackIDs := make(map[format.Format]int)
	for _, media := range medias {
		for _, mediaFormat := range media.Formats {
			id := len(tracks)
			tracks = append(tracks, rtspTrackInfo(id, media.Type, mediaFormat))
			trackIDs[mediaFormat] = id
		}
	}

	return tracks, trackIDs
}

func rtspTrackInfo(id int, mediaType description.MediaType, mediaFormat format.Format) Track {
	info := Track{ID: id, ClockRate: mediaFormat.ClockRate()}
	switch mediaType {
	case description.MediaTypeVideo:
		info.MediaType = MediaTypeVideo
	case description.MediaTypeAudio:
		info.MediaType = MediaTypeAudio
	default:
		info.MediaType = MediaTypeData
	}

	switch typed := mediaFormat.(type) {
	case *format.AV1:
		info.Codec = CodecAV1
	case *format.VP9:
		info.Codec = CodecVP9
	case *format.VP8:
		info.Codec = CodecVP8
	case *format.H265:
		info.Codec = CodecH265
		setH265Parameters(&info, typed.SPS)
	case *format.H264:
		info.Codec = CodecH264
		setH264Parameters(&info, typed.SPS)
	case *format.MPEG4Video:
		info.Codec = CodecMPEG4Video
	case *format.MPEG1Video:
		info.Codec = CodecMPEG1Video
	case *format.MJPEG:
		info.Codec = CodecJPEG
	case *format.Opus:
		info.Codec, info.SampleRate, info.Channels = CodecOpus, 48_000, typed.ChannelCount
	case *format.MPEG4Audio:
		info.Codec = CodecAAC
		if typed.Config != nil {
			info.SampleRate = typed.Config.SampleRate
			info.Channels = mpeg4AudioChannelCount(typed.Config.ChannelConfig)
		}
	case *format.MPEG1Audio:
		info.Codec = CodecMPEG1Audio
	case *format.AC3:
		info.Codec, info.SampleRate, info.Channels = CodecAC3, typed.SampleRate, typed.ChannelCount
	case *format.G711:
		info.Codec, info.SampleRate, info.Channels = CodecG711, typed.SampleRate, typed.ChannelCount
	case *format.LPCM:
		info.Codec, info.SampleRate, info.Channels = CodecLPCM, typed.SampleRate, typed.ChannelCount
	case *format.KLV:
		info.Codec = CodecKLV
	default:
		info.Codec = Codec(fmt.Sprintf("rtp-%d", mediaFormat.PayloadType()))
	}

	return info
}

type rtpTrackState struct {
	timestamp    uint32
	timestampSet bool
	size         int
	h264         *rtspH264State
}

type rtspH264State struct {
	decoder *rtph264.Decoder
	checker *h264DTSChecker

	timestamp    uint32
	pts          int64
	timestampSet bool
}

type rtpAggregator struct {
	stream *liveStream
	client *gortsplib.Client
	ids    map[format.Format]int
	mutex  sync.Mutex
	states map[format.Format]*rtpTrackState
}

func newRTPAggregator(
	stream *liveStream,
	client *gortsplib.Client,
	ids map[format.Format]int,
) (*rtpAggregator, error) {
	states := make(map[format.Format]*rtpTrackState, len(ids))
	for mediaFormat := range ids {
		state := &rtpTrackState{}
		if typed, ok := mediaFormat.(*format.H264); ok {
			decoder, err := typed.CreateDecoder()
			if err != nil {
				return nil, err
			}
			state.h264 = &rtspH264State{
				decoder: decoder,
				checker: newH264DTSChecker(typed.ClockRate(), typed.SPS, typed.PPS),
			}
		}
		states[mediaFormat] = state
	}

	return &rtpAggregator{
		stream: stream,
		client: client,
		ids:    ids,
		states: states,
	}, nil
}

func (a *rtpAggregator) handle(
	media *description.Media,
	mediaFormat format.Format,
	packet *rtp.Packet,
) {
	pts, ok := a.client.PacketPTS(media, packet)
	if !ok {
		return
	}

	a.mutex.Lock()
	state := a.states[mediaFormat]
	if state == nil {
		a.mutex.Unlock()
		return
	}

	if state.h264 != nil {
		sample, publish := a.handleH264(state.h264, mediaFormat, packet, pts)
		a.mutex.Unlock()
		if publish {
			a.stream.publishSample(sample)
		}
		return
	}

	if isRTSPVideoFormat(mediaFormat) {
		if !state.timestampSet || packet.Timestamp != state.timestamp {
			state.timestamp = packet.Timestamp
			state.timestampSet = true
			state.size = 0
		}
		state.size += len(packet.Payload)
		if !packet.Marker {
			a.mutex.Unlock()
			return
		}
	}

	size := len(packet.Payload)
	if isRTSPVideoFormat(mediaFormat) {
		size = state.size
		state.size = 0
	}
	sample := Sample{
		TrackID: a.ids[mediaFormat],
		PTS:     ticksToDuration(pts, mediaFormat.ClockRate()),
		DTS:     ticksToDuration(pts, mediaFormat.ClockRate()),
		Size:    size,
	}
	a.mutex.Unlock()
	a.stream.publishSample(sample)
}

func (a *rtpAggregator) handleH264(
	state *rtspH264State,
	mediaFormat format.Format,
	packet *rtp.Packet,
	pts int64,
) (Sample, bool) {
	samplePTS := pts
	if state.timestampSet && packet.Timestamp != state.timestamp {
		samplePTS = state.pts
	}

	accessUnit, err := state.decoder.Decode(packet)
	state.timestamp = packet.Timestamp
	state.pts = pts
	state.timestampSet = true
	if err != nil {
		return Sample{}, false
	}

	size, ok := buffersSize(accessUnit)
	if !ok {
		a.stream.fail(ErrInvalidSample)
		return Sample{}, false
	}
	report := state.checker.observe(samplePTS, accessUnit)
	timestamp := ticksToDuration(samplePTS, mediaFormat.ClockRate())
	return Sample{
		TrackID:        a.ids[mediaFormat],
		PTS:            timestamp,
		DTS:            timestamp,
		IsKeyFrame:     h264.IsRandomAccess(accessUnit),
		Size:           size,
		h264DTS:        report,
		h264DTSChecked: true,
	}, true
}

func isRTSPVideoFormat(mediaFormat format.Format) bool {
	switch mediaFormat.(type) {
	case *format.AV1, *format.VP9, *format.VP8, *format.H265, *format.H264,
		*format.MPEG4Video, *format.MPEG1Video, *format.MJPEG:
		return true
	default:
		return false
	}
}
