package mediaprobe_test

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/zbiljic/mediaprobe"
)

func ExampleProbeStream() {
	stream := &exampleStream{
		samples: []mediaprobe.Sample{
			{TrackID: 1, PTS: 0, DTS: 0, Size: 1_000},
			{TrackID: 1, PTS: time.Second, DTS: time.Second, Size: 1_000},
			{TrackID: 1, PTS: 2 * time.Second, DTS: 2 * time.Second, Size: 1_000},
		},
	}

	report, err := mediaprobe.ProbeStream(
		context.Background(),
		stream,
		mediaprobe.WithObservationDuration(2*time.Second),
	)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	track := report.Tracks[0]
	normalizeInCaller := track.IsVariableFrameRate ||
		track.NonMonotonicDTS > 0 ||
		track.TimestampDiscontinuities > 0

	fmt.Println("complete:", report.IsComplete)
	fmt.Println("samples:", track.SampleCount)
	fmt.Printf("observed frame rate: %.1f\n", track.ObservedFrameRate)
	fmt.Println("normalize in caller:", normalizeInCaller)

	// Output:
	// complete: true
	// samples: 3
	// observed frame rate: 1.0
	// normalize in caller: false
}

var _ mediaprobe.Stream = (*exampleStream)(nil)

type exampleStream struct {
	samples []mediaprobe.Sample
	next    int
}

func (*exampleStream) Tracks(context.Context) ([]mediaprobe.Track, error) {
	return []mediaprobe.Track{{
		ID:                1,
		MediaType:         mediaprobe.MediaTypeVideo,
		Codec:             mediaprobe.CodecH264,
		DeclaredFrameRate: mediaprobe.Rate{Numerator: 1, Denominator: 1},
	}}, nil
}

func (s *exampleStream) ReadSample(context.Context) (mediaprobe.Sample, error) {
	if s.next == len(s.samples) {
		return mediaprobe.Sample{}, io.EOF
	}

	sample := s.samples[s.next]
	s.next++
	return sample, nil
}
