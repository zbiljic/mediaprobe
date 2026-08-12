package mediaprobe

import (
	"encoding/json"
	"time"
)

// ReportSchemaVersion is the version of the JSON representation produced for
// Report values.
const ReportSchemaVersion = "0.1"

// TrackReport contains measurements for one track.
type TrackReport struct {
	Track
	SampleCount               int           `json:"sample_count"`
	ObservedPTSSpan           time.Duration `json:"observed_pts_span"`
	ObservedDTSSpan           time.Duration `json:"observed_dts_span"`
	Bitrate                   uint64        `json:"bitrate,omitempty"`
	ObservedFrameRate         float64       `json:"fps,omitempty"`
	NominalFrameDuration      time.Duration `json:"nominal_frame_duration,omitempty"`
	MaxFrameDurationDeviation time.Duration `json:"max_frame_duration_deviation,omitempty"`
	IrregularFrameIntervals   int           `json:"irregular_frame_intervals,omitempty"`
	NonMonotonicDTS           int           `json:"non_monotonic_dts,omitempty"`
	TimestampDiscontinuities  int           `json:"timestamp_discontinuities,omitempty"`
	IsVariableFrameRate       bool          `json:"variable_frame_rate,omitempty"`
	H264                      *H264Report   `json:"h264,omitempty"`
}

// H264Report contains diagnostics that require access to H.264 access units.
type H264Report struct {
	DTS H264DTSReport `json:"dts"`
}

// H264DTSReport describes attempts to derive DTS from H.264 picture order and
// PTS, as required by muxers that do not retain source-provided DTS.
type H264DTSReport struct {
	// Samples is the number of access units examined. Examination stops after
	// the first error.
	Samples int           `json:"samples"`
	Error   *H264DTSError `json:"error,omitempty"`
}

// H264DTSErrorCode identifies a DTS derivation failure without requiring
// callers to parse the implementation-specific error message.
type H264DTSErrorCode string

const (
	H264DTSErrorTooManyReorderedFrames H264DTSErrorCode = "too_many_reordered_frames"
	H264DTSErrorNonMonotonic           H264DTSErrorCode = "non_monotonic_dts"
	H264DTSErrorAfterPTS               H264DTSErrorCode = "dts_after_pts"
	H264DTSErrorUnsupportedPOC         H264DTSErrorCode = "unsupported_picture_order"
	H264DTSErrorMissingSPS             H264DTSErrorCode = "missing_sps"
	H264DTSErrorInvalidSPS             H264DTSErrorCode = "invalid_sps"
	H264DTSErrorOther                  H264DTSErrorCode = "other"
)

// H264DTSError describes the first DTS derivation failure observed for a
// track. At is elapsed media time from the first received access unit.
type H264DTSError struct {
	At      time.Duration    `json:"at"`
	Code    H264DTSErrorCode `json:"code"`
	Message string           `json:"message"`
}

// Report contains measurements from one stream incarnation.
type Report struct {
	Tracks              []TrackReport `json:"tracks"`
	ObservationDuration time.Duration `json:"observation_duration"`
	IsComplete          bool          `json:"complete"`
}

// MarshalJSON returns the stable JSON representation of a probe report. The Go
// model remains flat for convenient caller access, while the wire model groups
// declared media metadata separately from measurements made during probing.
func (r Report) MarshalJSON() ([]byte, error) {
	tracks := make([]reportTrackJSON, 0, len(r.Tracks))
	for _, track := range r.Tracks {
		tracks = append(tracks, newReportTrackJSON(track))
	}

	return json.Marshal(reportJSON{
		SchemaVersion: ReportSchemaVersion,
		Observation: reportObservationJSON{
			DurationNS: r.ObservationDuration,
			Complete:   r.IsComplete,
		},
		Tracks: tracks,
	})
}

type reportJSON struct {
	SchemaVersion string                `json:"schema_version"`
	Observation   reportObservationJSON `json:"observation"`
	Tracks        []reportTrackJSON     `json:"tracks"`
}

type reportObservationJSON struct {
	DurationNS time.Duration `json:"duration_ns"`
	Complete   bool          `json:"complete"`
}

type reportTrackJSON struct {
	ID           int                    `json:"id"`
	MediaType    MediaType              `json:"type"`
	Codec        Codec                  `json:"codec"`
	ClockRateHz  int                    `json:"clock_rate_hz,omitempty"`
	Video        *reportVideoJSON       `json:"video,omitempty"`
	Audio        *reportAudioJSON       `json:"audio,omitempty"`
	Measurements reportMeasurementsJSON `json:"measurements"`
	H264         *reportH264JSON        `json:"h264,omitempty"`
}

type reportVideoJSON struct {
	Width     int  `json:"width,omitempty"`
	Height    int  `json:"height,omitempty"`
	FrameRate Rate `json:"frame_rate,omitzero"`
}

type reportAudioJSON struct {
	SampleRateHz int `json:"sample_rate_hz,omitempty"`
	Channels     int `json:"channels,omitempty"`
}

type reportMeasurementsJSON struct {
	SampleCount                 int           `json:"sample_count"`
	PTSSpanNS                   time.Duration `json:"pts_span_ns"`
	DTSSpanNS                   time.Duration `json:"dts_span_ns"`
	AverageBitRateBPS           uint64        `json:"average_bit_rate_bps,omitempty"`
	AverageFrameRate            float64       `json:"average_frame_rate,omitempty"`
	NominalFrameDurationNS      time.Duration `json:"nominal_frame_duration_ns,omitempty"`
	MaxFrameDurationDeviationNS time.Duration `json:"max_frame_duration_deviation_ns,omitempty"`
	IrregularFrameIntervalCount int           `json:"irregular_frame_interval_count,omitempty"`
	NonMonotonicDTSCount        int           `json:"non_monotonic_dts_count,omitempty"`
	TimestampDiscontinuityCount int           `json:"timestamp_discontinuity_count,omitempty"`
	VariableFrameRate           *bool         `json:"variable_frame_rate,omitempty"`
}

type reportH264JSON struct {
	DTS reportH264DTSJSON `json:"dts"`
}

type reportH264DTSJSON struct {
	SamplesExamined int                     `json:"samples_examined"`
	Error           *reportH264DTSErrorJSON `json:"error,omitempty"`
}

type reportH264DTSErrorJSON struct {
	Code    H264DTSErrorCode        `json:"code"`
	Message string                  `json:"message"`
	Details []reportErrorDetailJSON `json:"details,omitempty"`
}

type reportErrorDetailJSON interface {
	reportErrorDetail()
}

type reportErrorDetailType string

const reportErrorDetailTypeMediaLocation reportErrorDetailType = "media_location"

type reportMediaLocationDetailJSON struct {
	Type         reportErrorDetailType `json:"type"`
	TimeOffsetNS time.Duration         `json:"time_offset_ns"`
}

func (reportMediaLocationDetailJSON) reportErrorDetail() {}

func newReportTrackJSON(track TrackReport) reportTrackJSON {
	wire := reportTrackJSON{
		ID:          track.ID,
		MediaType:   track.MediaType,
		Codec:       track.Codec,
		ClockRateHz: track.ClockRate,
		Measurements: reportMeasurementsJSON{
			SampleCount:                 track.SampleCount,
			PTSSpanNS:                   track.ObservedPTSSpan,
			DTSSpanNS:                   track.ObservedDTSSpan,
			AverageBitRateBPS:           track.Bitrate,
			AverageFrameRate:            track.ObservedFrameRate,
			NominalFrameDurationNS:      track.NominalFrameDuration,
			MaxFrameDurationDeviationNS: track.MaxFrameDurationDeviation,
			IrregularFrameIntervalCount: track.IrregularFrameIntervals,
			NonMonotonicDTSCount:        track.NonMonotonicDTS,
			TimestampDiscontinuityCount: track.TimestampDiscontinuities,
		},
	}

	switch track.MediaType {
	case MediaTypeVideo:
		wire.Video = &reportVideoJSON{
			Width:     track.Width,
			Height:    track.Height,
			FrameRate: track.DeclaredFrameRate,
		}
		if track.ObservedFrameRate > 0 || track.NominalFrameDuration > 0 {
			wire.Measurements.VariableFrameRate = &track.IsVariableFrameRate
		}
	case MediaTypeAudio:
		wire.Audio = &reportAudioJSON{
			SampleRateHz: track.SampleRate,
			Channels:     track.Channels,
		}
	}

	if track.H264 != nil {
		wire.H264 = &reportH264JSON{DTS: reportH264DTSJSON{
			SamplesExamined: track.H264.DTS.Samples,
		}}
		if track.H264.DTS.Error != nil {
			wire.H264.DTS.Error = &reportH264DTSErrorJSON{
				Code:    track.H264.DTS.Error.Code,
				Message: track.H264.DTS.Error.Message,
				Details: []reportErrorDetailJSON{reportMediaLocationDetailJSON{
					Type:         reportErrorDetailTypeMediaLocation,
					TimeOffsetNS: track.H264.DTS.Error.At,
				}},
			}
		}
	}

	return wire
}
