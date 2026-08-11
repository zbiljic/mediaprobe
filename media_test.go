package mediaprobe_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/zbiljic/mediaprobe"
)

func TestRate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		rate  mediaprobe.Rate
		valid bool
		value float64
		text  string
	}{
		{name: "zero"},
		{
			name:  "ntsc",
			rate:  mediaprobe.Rate{Numerator: 30_000, Denominator: 1_001},
			valid: true,
			value: 30_000.0 / 1_001.0,
			text:  "30000/1001",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.rate.IsValid(); got != test.valid {
				t.Fatalf("IsValid() = %t, want %t", got, test.valid)
			}
			if got := test.rate.Float64(); math.Abs(got-test.value) > 1e-9 {
				t.Fatalf("Float64() = %v, want %v", got, test.value)
			}
			if got := test.rate.String(); got != test.text {
				t.Fatalf("String() = %q, want %q", got, test.text)
			}
		})
	}
}

func TestTrackJSON(t *testing.T) {
	t.Parallel()

	track := mediaprobe.Track{
		ID:                7,
		MediaType:         mediaprobe.MediaTypeVideo,
		Codec:             mediaprobe.CodecH264,
		ClockRate:         90_000,
		Width:             1920,
		Height:            1080,
		DeclaredFrameRate: mediaprobe.Rate{Numerator: 30_000, Denominator: 1_001},
	}

	encoded, err := json.Marshal(track)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	const want = `{"id":7,"type":"video","codec":"h264","clock_rate":90000,"width":1920,"height":1080,"declared_frame_rate":{"numerator":30000,"denominator":1001}}`
	if got := string(encoded); got != want {
		t.Fatalf("Marshal() = %s, want %s", got, want)
	}
}
