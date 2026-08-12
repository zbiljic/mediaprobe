package mediaprobe_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/zbiljic/mediaprobe"
)

func TestProbeStream(t *testing.T) {
	t.Parallel()

	const trackID = 7
	stream := &memoryStream{
		tracks: []mediaprobe.Track{{
			ID:                trackID,
			MediaType:         mediaprobe.MediaTypeVideo,
			Codec:             mediaprobe.CodecH264,
			ClockRate:         90_000,
			Width:             1920,
			Height:            1080,
			DeclaredFrameRate: mediaprobe.Rate{Numerator: 30_000, Denominator: 1_001},
		}},
		samples: constantRateSamples(
			trackID,
			30_000,
			1_001,
			mediaprobe.DefaultObservationDuration,
		),
	}
	for index := range stream.samples {
		stream.samples[index].Size = 1_000
	}

	report, err := mediaprobe.ProbeStream(t.Context(), stream)
	requireNoError(t, err)
	requireEqual(t, report.IsComplete, true)
	requireEqual(t, report.ObservationDuration, mediaprobe.DefaultObservationDuration)
	requireEqual(t, len(report.Tracks), 1)
	requireEqual(t, report.Tracks[0].ObservedDTSSpan, report.Tracks[0].ObservedPTSSpan)
	requireNear(t, report.Tracks[0].ObservedFrameRate, 30_000.0/1_001.0, 0.01)
	expectedBitrate := uint64(
		float64(len(stream.samples)*1_000*8) / report.Tracks[0].ObservedPTSSpan.Seconds(),
	)
	requireEqual(t, report.Tracks[0].Bitrate, expectedBitrate)
	requireEqual(t, report.Tracks[0].IsVariableFrameRate, false)
}

func TestProbeWithObservationDuration(t *testing.T) {
	t.Parallel()

	const observationDuration = 2 * time.Second
	stream := &memoryStream{
		tracks: []mediaprobe.Track{{
			ID:        1,
			MediaType: mediaprobe.MediaTypeVideo,
			Codec:     mediaprobe.CodecH264,
		}},
		samples: []mediaprobe.Sample{
			{TrackID: 1, PTS: 0, DTS: 0},
			{TrackID: 1, PTS: time.Second, DTS: time.Second},
			{TrackID: 1, PTS: 2 * time.Second, DTS: 2 * time.Second},
			{TrackID: 1, PTS: 3 * time.Second, DTS: 3 * time.Second},
		},
	}

	report, err := mediaprobe.ProbeStream(
		t.Context(),
		stream,
		mediaprobe.WithObservationDuration(observationDuration),
	)
	requireNoError(t, err)
	requireEqual(t, report.IsComplete, true)
	requireEqual(t, report.ObservationDuration, observationDuration)
	requireEqual(t, stream.readSamples, 3)
}

func TestProbeRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	t.Run("zero duration", func(t *testing.T) {
		t.Parallel()
		_, err := mediaprobe.ProbeStream(
			t.Context(),
			&memoryStream{},
			mediaprobe.WithObservationDuration(0),
		)
		requireErrorIs(t, err, mediaprobe.ErrInvalidObservationDuration)
	})

	t.Run("negative duration", func(t *testing.T) {
		t.Parallel()
		_, err := mediaprobe.ProbeStream(
			t.Context(),
			&memoryStream{},
			mediaprobe.WithObservationDuration(-time.Second),
		)
		requireErrorIs(t, err, mediaprobe.ErrInvalidObservationDuration)
	})

	t.Run("zero sample limit", func(t *testing.T) {
		t.Parallel()
		_, err := mediaprobe.ProbeStream(
			t.Context(),
			&memoryStream{},
			mediaprobe.WithSampleLimit(0),
		)
		requireErrorIs(t, err, mediaprobe.ErrInvalidSampleLimit)
	})

	t.Run("negative sample limit", func(t *testing.T) {
		t.Parallel()
		_, err := mediaprobe.ProbeStream(
			t.Context(),
			&memoryStream{},
			mediaprobe.WithSampleLimit(-1),
		)
		requireErrorIs(t, err, mediaprobe.ErrInvalidSampleLimit)
	})

	t.Run("nil stream", func(t *testing.T) {
		t.Parallel()
		_, err := mediaprobe.ProbeStream(t.Context(), nil)
		requireErrorIs(t, err, mediaprobe.ErrNoTracks)
	})

	t.Run("no tracks", func(t *testing.T) {
		t.Parallel()
		_, err := mediaprobe.ProbeStream(t.Context(), &memoryStream{})
		requireErrorIs(t, err, mediaprobe.ErrNoTracks)
	})
}

func TestProbeRejectsInvalidTracks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		tracks []mediaprobe.Track
	}{
		{
			name:   "unknown media type",
			tracks: []mediaprobe.Track{{ID: 1, MediaType: mediaprobe.MediaTypeUnknown, Codec: mediaprobe.CodecH264}},
		},
		{
			name:   "missing codec",
			tracks: []mediaprobe.Track{{ID: 1, MediaType: mediaprobe.MediaTypeVideo}},
		},
		{
			name: "duplicate id",
			tracks: []mediaprobe.Track{
				{ID: 1, MediaType: mediaprobe.MediaTypeVideo, Codec: mediaprobe.CodecH264},
				{ID: 1, MediaType: mediaprobe.MediaTypeAudio, Codec: mediaprobe.CodecAAC},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := mediaprobe.ProbeStream(t.Context(), &memoryStream{tracks: test.tracks})
			requireErrorIs(t, err, mediaprobe.ErrInvalidTrack)
		})
	}
}

func TestReportJSON(t *testing.T) {
	t.Parallel()

	report := mediaprobe.Report{
		Tracks: []mediaprobe.TrackReport{
			{
				Track: mediaprobe.Track{
					ID:                1,
					MediaType:         mediaprobe.MediaTypeVideo,
					Codec:             mediaprobe.CodecH264,
					ClockRate:         90_000,
					Width:             1920,
					Height:            1080,
					DeclaredFrameRate: mediaprobe.Rate{Numerator: 25, Denominator: 1},
				},
				SampleCount:               451,
				ObservedPTSSpan:           15 * time.Second,
				ObservedDTSSpan:           15 * time.Second,
				ObservedFrameRate:         25,
				Bitrate:                   3_124_000,
				NominalFrameDuration:      40 * time.Millisecond,
				MaxFrameDurationDeviation: time.Millisecond,
				IrregularFrameIntervals:   2,
				NonMonotonicDTS:           1,
				TimestampDiscontinuities:  3,
				IsVariableFrameRate:       true,
				H264: &mediaprobe.H264Report{DTS: mediaprobe.H264DTSReport{
					Samples: 451,
					Error: &mediaprobe.H264DTSError{
						At:      22480 * time.Millisecond,
						Code:    mediaprobe.H264DTSErrorTooManyReorderedFrames,
						Message: "too many reordered frames (49)",
					},
				}},
			},
			{
				Track: mediaprobe.Track{
					ID:         2,
					MediaType:  mediaprobe.MediaTypeAudio,
					Codec:      mediaprobe.CodecAAC,
					SampleRate: 48_000,
					Channels:   2,
				},
				SampleCount:     704,
				ObservedPTSSpan: 15 * time.Second,
				ObservedDTSSpan: 15 * time.Second,
			},
		},
		ObservationDuration: mediaprobe.DefaultObservationDuration,
		IsComplete:          true,
	}

	encoded, err := json.Marshal(report)
	requireNoError(t, err)
	requireJSONEqual(t, string(encoded), `{
		"schema_version": "0.1",
		"observation": {
			"duration_ns": 15000000000,
			"complete": true
		},
		"tracks": [{
			"id": 1,
			"type": "video",
			"codec": "h264",
			"clock_rate_hz": 90000,
			"video": {
				"width": 1920,
				"height": 1080,
				"frame_rate": {
					"numerator": 25,
					"denominator": 1
				}
			},
			"measurements": {
				"sample_count": 451,
				"pts_span_ns": 15000000000,
				"dts_span_ns": 15000000000,
				"average_bit_rate_bps": 3124000,
				"average_frame_rate": 25,
				"nominal_frame_duration_ns": 40000000,
				"max_frame_duration_deviation_ns": 1000000,
				"irregular_frame_interval_count": 2,
				"non_monotonic_dts_count": 1,
				"timestamp_discontinuity_count": 3,
				"variable_frame_rate": true
			},
			"h264": {
				"dts": {
					"samples_examined": 451,
					"error": {
						"code": "too_many_reordered_frames",
						"message": "too many reordered frames (49)",
						"details": [{
							"type": "media_location",
							"time_offset_ns": 22480000000
						}]
					}
				}
			}
		}, {
			"id": 2,
			"type": "audio",
			"codec": "aac",
			"audio": {
				"sample_rate_hz": 48000,
				"channels": 2
			},
			"measurements": {
				"sample_count": 704,
				"pts_span_ns": 15000000000,
				"dts_span_ns": 15000000000
			}
		}]
	}`)
}

func TestPartialReportJSON(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(mediaprobe.Report{
		ObservationDuration: 2 * time.Second,
	})
	requireNoError(t, err)
	requireJSONEqual(t, string(encoded), `{
		"schema_version": "0.1",
		"observation": {
			"duration_ns": 2000000000,
			"complete": false
		},
		"tracks": []
	}`)
}

func TestReportJSONPreservesZeroErrorOffset(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(mediaprobe.Report{Tracks: []mediaprobe.TrackReport{{
		Track: mediaprobe.Track{ID: 1, MediaType: mediaprobe.MediaTypeVideo, Codec: mediaprobe.CodecH264},
		H264: &mediaprobe.H264Report{DTS: mediaprobe.H264DTSReport{
			Samples: 1,
			Error: &mediaprobe.H264DTSError{
				Code:    mediaprobe.H264DTSErrorMissingSPS,
				Message: "SPS not received yet",
			},
		}},
	}}})
	requireNoError(t, err)

	var report struct {
		Tracks []struct {
			H264 struct {
				DTS struct {
					Error struct {
						Details []struct {
							TimeOffsetNS *int64 `json:"time_offset_ns"`
						} `json:"details"`
					} `json:"error"`
				} `json:"dts"`
			} `json:"h264"`
		} `json:"tracks"`
	}
	requireNoError(t, json.Unmarshal(encoded, &report))
	if offset := report.Tracks[0].H264.DTS.Error.Details[0].TimeOffsetNS; offset == nil || *offset != 0 {
		t.Fatalf("time_offset_ns = %v, want pointer to zero", offset)
	}
}

func TestProbeDetectsTimingProblems(t *testing.T) {
	t.Parallel()

	t.Run("variable cadence", func(t *testing.T) {
		t.Parallel()

		const trackID = 1
		var samples []mediaprobe.Sample
		var timestamp time.Duration
		for index := 0; timestamp <= mediaprobe.DefaultObservationDuration; index++ {
			samples = append(samples, mediaprobe.Sample{TrackID: trackID, PTS: timestamp, DTS: timestamp})
			if index%2 == 0 {
				timestamp += 20 * time.Millisecond
			} else {
				timestamp += 50 * time.Millisecond
			}
		}

		report, err := mediaprobe.ProbeStream(t.Context(), &memoryStream{
			tracks:  []mediaprobe.Track{{ID: trackID, MediaType: mediaprobe.MediaTypeVideo, Codec: mediaprobe.CodecH264}},
			samples: samples,
		})
		requireNoError(t, err)
		requireEqual(t, report.Tracks[0].IsVariableFrameRate, true)
		requireEqual(t, report.Tracks[0].IrregularFrameIntervals > 0, true)
	})

	t.Run("timestamp discontinuity", func(t *testing.T) {
		t.Parallel()

		var samples []mediaprobe.Sample
		for timestamp := time.Duration(0); timestamp <= time.Second; timestamp += 40 * time.Millisecond {
			samples = append(samples, mediaprobe.Sample{TrackID: 1, PTS: timestamp, DTS: timestamp})
		}
		samples = append(samples, mediaprobe.Sample{
			TrackID: 1,
			PTS:     mediaprobe.DefaultObservationDuration,
			DTS:     mediaprobe.DefaultObservationDuration,
		})

		report, err := mediaprobe.ProbeStream(t.Context(), &memoryStream{
			tracks:  []mediaprobe.Track{{ID: 1, MediaType: mediaprobe.MediaTypeVideo, Codec: mediaprobe.CodecH264}},
			samples: samples,
		})
		requireNoError(t, err)
		requireEqual(t, report.Tracks[0].TimestampDiscontinuities, 1)
	})

	t.Run("non-monotonic dts", func(t *testing.T) {
		t.Parallel()

		report, err := mediaprobe.ProbeStream(t.Context(), &memoryStream{
			tracks: []mediaprobe.Track{{ID: 1, MediaType: mediaprobe.MediaTypeVideo, Codec: mediaprobe.CodecH264}},
			samples: []mediaprobe.Sample{
				{TrackID: 1, PTS: 0, DTS: 0},
				{TrackID: 1, PTS: time.Second, DTS: time.Second},
				{TrackID: 1, PTS: 2 * time.Second, DTS: 500 * time.Millisecond},
				{TrackID: 1, PTS: 15 * time.Second, DTS: 15 * time.Second},
			},
		})
		requireNoError(t, err)
		requireEqual(t, report.Tracks[0].NonMonotonicDTS, 1)
	})
}

func TestProbeUsesVideoAsObservationClock(t *testing.T) {
	t.Parallel()

	stream := &memoryStream{
		tracks: []mediaprobe.Track{
			{ID: 1, MediaType: mediaprobe.MediaTypeAudio, Codec: mediaprobe.CodecOpus},
			{ID: 2, MediaType: mediaprobe.MediaTypeVideo, Codec: mediaprobe.CodecVP9},
		},
		samples: []mediaprobe.Sample{
			{TrackID: 1, PTS: 0, DTS: 0},
			{TrackID: 1, PTS: 20 * time.Second, DTS: 20 * time.Second},
			{TrackID: 2, PTS: 0, DTS: 0},
			{TrackID: 2, PTS: 15 * time.Second, DTS: 15 * time.Second},
		},
	}

	report, err := mediaprobe.ProbeStream(t.Context(), stream)
	requireNoError(t, err)
	requireEqual(t, stream.readSamples, 4)
	requireEqual(t, report.IsComplete, true)
	requireEqual(t, report.Tracks[0].ObservedFrameRate, 0.0)
}

func TestProbeReturnsPartialReport(t *testing.T) {
	t.Parallel()

	stream := &memoryStream{
		tracks: []mediaprobe.Track{{ID: 1, MediaType: mediaprobe.MediaTypeVideo, Codec: mediaprobe.CodecH264}},
		samples: []mediaprobe.Sample{
			{TrackID: 1, PTS: 0, DTS: 0},
			{TrackID: 1, PTS: 10 * time.Second, DTS: 10 * time.Second},
		},
	}

	report, err := mediaprobe.ProbeStream(t.Context(), stream)
	requireErrorIs(t, err, mediaprobe.ErrObservationIncomplete)
	requireEqual(t, report.IsComplete, false)
	requireEqual(t, report.Tracks[0].ObservedPTSSpan, 10*time.Second)
}

func TestProbeStopsAtSampleLimit(t *testing.T) {
	t.Parallel()

	stream := &memoryStream{
		tracks: []mediaprobe.Track{{ID: 1, MediaType: mediaprobe.MediaTypeVideo, Codec: mediaprobe.CodecH264}},
		samples: []mediaprobe.Sample{
			{TrackID: 1},
			{TrackID: 1},
			{TrackID: 1},
		},
	}

	report, err := mediaprobe.ProbeStream(
		t.Context(),
		stream,
		mediaprobe.WithSampleLimit(2),
	)
	requireErrorIs(t, err, mediaprobe.ErrSampleLimitExceeded)
	requireEqual(t, report.IsComplete, false)
	requireEqual(t, report.Tracks[0].SampleCount, 2)
	requireEqual(t, stream.readSamples, 3)
}

func TestProbeRejectsCumulativeSampleSizeOverflow(t *testing.T) {
	t.Parallel()

	if ^uint(0)>>63 == 0 {
		t.Skip("sample sizes cannot overflow uint64 on a 32-bit platform within the sample limit")
	}

	maximumInt := int(^uint(0) >> 1)
	stream := &memoryStream{
		tracks: []mediaprobe.Track{{ID: 1, MediaType: mediaprobe.MediaTypeAudio, Codec: mediaprobe.CodecAAC}},
		samples: []mediaprobe.Sample{
			{TrackID: 1, Size: maximumInt},
			{TrackID: 1, Size: maximumInt},
			{TrackID: 1, Size: 2},
		},
	}

	report, err := mediaprobe.ProbeStream(
		t.Context(),
		stream,
		mediaprobe.WithSampleLimit(3),
	)
	requireErrorIs(t, err, mediaprobe.ErrInvalidSample)
	requireEqual(t, report.Tracks[0].SampleCount, 2)
}

func TestProbeRejectsInvalidSamples(t *testing.T) {
	t.Parallel()

	t.Run("unknown track", func(t *testing.T) {
		t.Parallel()
		_, err := mediaprobe.ProbeStream(t.Context(), &memoryStream{
			tracks:  []mediaprobe.Track{{ID: 1, MediaType: mediaprobe.MediaTypeAudio, Codec: mediaprobe.CodecOpus}},
			samples: []mediaprobe.Sample{{TrackID: 2}},
		})
		requireErrorIs(t, err, mediaprobe.ErrUnknownTrack)
	})

	t.Run("negative size", func(t *testing.T) {
		t.Parallel()
		_, err := mediaprobe.ProbeStream(t.Context(), &memoryStream{
			tracks:  []mediaprobe.Track{{ID: 1, MediaType: mediaprobe.MediaTypeAudio, Codec: mediaprobe.CodecOpus}},
			samples: []mediaprobe.Sample{{TrackID: 1, Size: -1}},
		})
		requireErrorIs(t, err, mediaprobe.ErrInvalidSample)
	})

	t.Run("negative duration", func(t *testing.T) {
		t.Parallel()
		_, err := mediaprobe.ProbeStream(t.Context(), &memoryStream{
			tracks:  []mediaprobe.Track{{ID: 1, MediaType: mediaprobe.MediaTypeAudio, Codec: mediaprobe.CodecOpus}},
			samples: []mediaprobe.Sample{{TrackID: 1, Duration: -time.Nanosecond}},
		})
		requireErrorIs(t, err, mediaprobe.ErrInvalidSample)
	})
}

func TestProbePropagatesErrors(t *testing.T) {
	t.Parallel()

	t.Run("context cancellation", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := mediaprobe.ProbeStream(ctx, &memoryStream{
			tracks: []mediaprobe.Track{{ID: 1, MediaType: mediaprobe.MediaTypeAudio, Codec: mediaprobe.CodecOpus}},
		})
		requireErrorIs(t, err, context.Canceled)
	})

	t.Run("track read", func(t *testing.T) {
		t.Parallel()

		trackErr := errors.New("track failure")
		_, err := mediaprobe.ProbeStream(t.Context(), &memoryStream{trackErr: trackErr})
		requireErrorIs(t, err, trackErr)
	})

	t.Run("sample read", func(t *testing.T) {
		t.Parallel()

		readErr := errors.New("sample failure")
		report, err := mediaprobe.ProbeStream(t.Context(), &memoryStream{
			tracks:  []mediaprobe.Track{{ID: 1, MediaType: mediaprobe.MediaTypeAudio, Codec: mediaprobe.CodecOpus}},
			readErr: readErr,
		})
		requireErrorIs(t, err, readErr)
		requireEqual(t, len(report.Tracks), 1)
	})
}

type memoryStream struct {
	tracks      []mediaprobe.Track
	samples     []mediaprobe.Sample
	trackErr    error
	readErr     error
	readSamples int
}

func (s *memoryStream) Tracks(context.Context) ([]mediaprobe.Track, error) {
	return s.tracks, s.trackErr
}

func (s *memoryStream) ReadSample(context.Context) (mediaprobe.Sample, error) {
	if s.readSamples >= len(s.samples) {
		if s.readErr != nil {
			return mediaprobe.Sample{}, s.readErr
		}
		return mediaprobe.Sample{}, io.EOF
	}

	sample := s.samples[s.readSamples]
	s.readSamples++
	return sample, nil
}

func constantRateSamples(
	trackID int,
	numerator uint32,
	denominator uint32,
	duration time.Duration,
) []mediaprobe.Sample {
	var samples []mediaprobe.Sample
	for frame := 0; ; frame++ {
		timestamp := time.Duration(
			int64(frame) * int64(time.Second) * int64(denominator) / int64(numerator),
		)
		samples = append(samples, mediaprobe.Sample{TrackID: trackID, PTS: timestamp, DTS: timestamp})
		if timestamp >= duration {
			return samples
		}
	}
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func requireErrorIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want errors.Is(%v)", err, target)
	}
}

func requireEqual[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func requireNear(t *testing.T, got, want, delta float64) {
	t.Helper()
	if math.Abs(got-want) > delta {
		t.Fatalf("got %v, want %v within %v", got, want, delta)
	}
}

func requireJSONEqual(t *testing.T, got, want string) {
	t.Helper()
	var gotValue any
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		t.Fatalf("unmarshal actual JSON: %v", err)
	}
	var wantValue any
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("unmarshal expected JSON: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
}

var _ mediaprobe.Stream = (*memoryStream)(nil)
