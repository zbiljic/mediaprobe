package mediaprobe

import (
	"time"
)

// TrackReport contains measurements for one track.
type TrackReport struct {
	Track
	SampleCount               int           `json:"sample_count"`
	ObservedDuration          time.Duration `json:"observed_duration"`
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
