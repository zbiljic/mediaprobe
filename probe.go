package mediaprobe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"time"
)

const (
	// DefaultObservationDuration is the media timeline covered by a probe when
	// no observation-duration option is provided.
	DefaultObservationDuration = 15 * time.Second
	// DefaultSampleLimit is the maximum number of sample records retained by a
	// probe when no sample-limit option is provided.
	DefaultSampleLimit = 100_000

	frameRateTolerance       = 0.05
	minimumCadenceTolerance  = time.Millisecond
	minimumDiscontinuityGap  = 500 * time.Millisecond
	discontinuityFrameFactor = 10
)

// Stream is the transport-neutral input consumed by ProbeStream.
// Implementations can wrap HLS, RTSP, RTMP, SRT, MPEG-TS, or another live
// demuxer. The caller retains ownership of the stream and is responsible for
// closing it when necessary.
type Stream interface {
	Tracks(context.Context) ([]Track, error)
	ReadSample(context.Context) (Sample, error)
}

// Option configures ProbeStream and ProbeURL.
type Option func(*probeOptions)

// WithObservationDuration overrides DefaultObservationDuration. The duration
// must be greater than zero.
func WithObservationDuration(duration time.Duration) Option {
	return func(options *probeOptions) {
		options.observationDuration = duration
	}
}

// WithSampleLimit sets the maximum number of sample records retained across
// all tracks. The limit must be greater than zero.
func WithSampleLimit(limit int) Option {
	return func(options *probeOptions) {
		options.sampleLimit = limit
	}
}

type probeOptions struct {
	observationDuration time.Duration
	sampleLimit         int
	http                httpOptions
}

// ProbeStream observes a stream until it covers the configured duration of
// media time. Callers should create a new Stream for each upstream incarnation
// or reconnect. On failure, the returned report contains any measurements
// collected so far.
func ProbeStream(ctx context.Context, stream Stream, options ...Option) (Report, error) {
	config := applyOptions(options)
	if err := validateOptions(config); err != nil {
		return Report{}, err
	}

	return probeStream(ctx, stream, config)
}

func probeStream(ctx context.Context, stream Stream, config probeOptions) (Report, error) {
	if stream == nil {
		return Report{}, fmt.Errorf("%w: nil stream", ErrNoTracks)
	}

	tracks, err := stream.Tracks(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("read tracks: %w", err)
	}

	probe, err := newProbe(tracks, config.observationDuration, config.sampleLimit)
	if err != nil {
		return Report{}, err
	}

	for !probe.isComplete() {
		if err := ctx.Err(); err != nil {
			return probe.report(), err
		}

		sample, readErr := stream.ReadSample(ctx)
		if readErr != nil {
			report := probe.report()
			if errors.Is(readErr, io.EOF) {
				return report, ErrObservationIncomplete
			}

			return report, fmt.Errorf("read sample: %w", readErr)
		}

		if err := probe.observe(sample); err != nil {
			return probe.report(), err
		}
	}

	return probe.report(), nil
}

func validateOptions(config probeOptions) error {
	if config.observationDuration <= 0 {
		return ErrInvalidObservationDuration
	}
	if config.sampleLimit <= 0 {
		return ErrInvalidSampleLimit
	}

	return nil
}

func applyOptions(options []Option) probeOptions {
	config := probeOptions{
		observationDuration: DefaultObservationDuration,
		sampleLimit:         DefaultSampleLimit,
	}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}

	return config
}

type trackSamples struct {
	track      Track
	samples    []Sample
	totalBytes uint64
}

type probeState struct {
	tracks              map[int]*trackSamples
	trackOrder          []int
	clockTrackID        int
	observationDuration time.Duration
	sampleLimit         int
	sampleCount         int
}

func newProbe(tracks []Track, observationDuration time.Duration, sampleLimit int) (*probeState, error) {
	if len(tracks) == 0 {
		return nil, ErrNoTracks
	}

	state := &probeState{
		tracks:              make(map[int]*trackSamples, len(tracks)),
		trackOrder:          make([]int, 0, len(tracks)),
		observationDuration: observationDuration,
		sampleLimit:         sampleLimit,
	}

	for _, track := range tracks {
		if track.MediaType == "" || track.MediaType == MediaTypeUnknown || track.Codec == "" {
			return nil, fmt.Errorf("%w: track %d", ErrInvalidTrack, track.ID)
		}
		if _, exists := state.tracks[track.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate track %d", ErrInvalidTrack, track.ID)
		}

		state.tracks[track.ID] = &trackSamples{
			track:   track,
			samples: []Sample{},
		}
		state.trackOrder = append(state.trackOrder, track.ID)
	}

	state.clockTrackID = state.trackOrder[0]
	for _, trackID := range state.trackOrder {
		if state.tracks[trackID].track.MediaType == MediaTypeVideo {
			state.clockTrackID = trackID
			break
		}
	}

	return state, nil
}

func (p *probeState) observe(sample Sample) error {
	if sample.Size < 0 || sample.Duration < 0 {
		return fmt.Errorf("%w: track %d", ErrInvalidSample, sample.TrackID)
	}

	track, ok := p.tracks[sample.TrackID]
	if !ok {
		return fmt.Errorf("%w: track %d", ErrUnknownTrack, sample.TrackID)
	}
	if p.sampleCount >= p.sampleLimit {
		return ErrSampleLimitExceeded
	}

	size := uint64(sample.Size)
	if size > ^uint64(0)-track.totalBytes {
		return fmt.Errorf("%w: cumulative size for track %d overflows", ErrInvalidSample, sample.TrackID)
	}

	track.samples = append(track.samples, sample)
	track.totalBytes += size
	p.sampleCount++
	return nil
}

func (p *probeState) isComplete() bool {
	track := p.tracks[p.clockTrackID]
	if len(track.samples) < 2 {
		return false
	}

	minimumDTS, maximumDTS := track.samples[0].DTS, track.samples[0].DTS
	for _, sample := range track.samples[1:] {
		minimumDTS = min(minimumDTS, sample.DTS)
		maximumDTS = max(maximumDTS, sample.DTS)
	}

	return maximumDTS-minimumDTS >= p.observationDuration
}

func (p *probeState) report() Report {
	reports := make([]TrackReport, 0, len(p.trackOrder))
	for _, trackID := range p.trackOrder {
		reports = append(reports, analyzeTrack(p.tracks[trackID]))
	}

	return Report{
		Tracks:              reports,
		ObservationDuration: p.observationDuration,
		IsComplete:          p.isComplete(),
	}
}

func analyzeTrack(track *trackSamples) TrackReport {
	report := TrackReport{
		Track:       track.track,
		SampleCount: len(track.samples),
	}
	if len(track.samples) < 2 {
		return report
	}

	pts := make([]time.Duration, 0, len(track.samples))
	for _, sample := range track.samples {
		pts = append(pts, sample.PTS)
	}
	slices.Sort(pts)

	report.ObservedDuration = pts[len(pts)-1] - pts[0]
	if report.ObservedDuration > 0 {
		bitrate := float64(track.totalBytes) * 8 / report.ObservedDuration.Seconds()
		if bitrate >= float64(^uint64(0)) {
			report.Bitrate = ^uint64(0)
		} else {
			report.Bitrate = uint64(bitrate)
		}
	}
	if report.ObservedDuration > 0 && track.track.MediaType == MediaTypeVideo {
		report.ObservedFrameRate = float64(len(pts)-1) / report.ObservedDuration.Seconds()
	}

	intervals := positiveIntervals(pts)
	if len(intervals) == 0 {
		return report
	}

	report.NominalFrameDuration = median(intervals)
	tolerance := max(minimumCadenceTolerance, report.NominalFrameDuration/20)
	for _, interval := range intervals {
		deviation := absoluteDuration(interval - report.NominalFrameDuration)
		report.MaxFrameDurationDeviation = max(report.MaxFrameDurationDeviation, deviation)
		if deviation > tolerance {
			report.IrregularFrameIntervals++
		}
	}

	maximumGap := max(
		minimumDiscontinuityGap,
		report.NominalFrameDuration*discontinuityFrameFactor,
	)
	for index := 1; index < len(track.samples); index++ {
		delta := track.samples[index].DTS - track.samples[index-1].DTS
		if delta < 0 {
			report.NonMonotonicDTS++
			continue
		}
		if delta > maximumGap {
			report.TimestampDiscontinuities++
		}
	}

	if track.track.MediaType == MediaTypeVideo {
		irregularRatio := float64(report.IrregularFrameIntervals) / float64(len(intervals))
		report.IsVariableFrameRate = irregularRatio > frameRateTolerance
		if declared := track.track.DeclaredFrameRate.Float64(); declared > 0 {
			difference := math.Abs(report.ObservedFrameRate-declared) / declared
			report.IsVariableFrameRate = report.IsVariableFrameRate || difference > frameRateTolerance
		}
	}

	return report
}

func positiveIntervals(timestamps []time.Duration) []time.Duration {
	intervals := make([]time.Duration, 0, len(timestamps)-1)
	for index := 1; index < len(timestamps); index++ {
		if interval := timestamps[index] - timestamps[index-1]; interval > 0 {
			intervals = append(intervals, interval)
		}
	}

	return intervals
}

func median(values []time.Duration) time.Duration {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 != 0 {
		return sorted[middle]
	}

	return sorted[middle-1] + (sorted[middle]-sorted[middle-1])/2
}

func absoluteDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}

	return value
}
