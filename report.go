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
}

// Report contains measurements from one stream incarnation.
type Report struct {
	Tracks              []TrackReport `json:"tracks"`
	ObservationDuration time.Duration `json:"observation_duration"`
	IsComplete          bool          `json:"complete"`
}
