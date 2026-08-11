package mediaprobe

import (
	"math"
	"testing"
	"time"
)

func TestH264DTSChecker(t *testing.T) {
	t.Parallel()

	checker := newH264DTSChecker(90_000, nil, nil)

	report := checker.observe(0, [][]byte{{0x41}})
	if report.Samples != 0 || report.Error != nil {
		t.Fatalf("report before IDR = %+v", report)
	}

	report = checker.observe(9_000, h264DTSFirstAccessUnit)
	if report.Samples != 1 || report.Error != nil {
		t.Fatalf("report after IDR = %+v, error = %+v", report, report.Error)
	}

	report = checker.observe(12_000, [][]byte{{0x41, 0x9a, 0x21, 0x6c, 0x45, 0xff}})
	if report.Samples != 2 || report.Error != nil {
		t.Fatalf("report after non-IDR = %+v", report)
	}
}

func TestH264DTSCheckerReportsFirstError(t *testing.T) {
	t.Parallel()

	checker := newH264DTSChecker(90_000, nil, nil)
	report := checker.observe(9_000, [][]byte{{0x65}})
	if report.Samples != 1 || report.Error == nil {
		t.Fatalf("report = %+v", report)
	}
	if report.Error.At != 0 || report.Error.Code != H264DTSErrorMissingSPS {
		t.Fatalf("error = %+v", report.Error)
	}

	report = checker.observe(18_000, h264DTSFirstAccessUnit)
	if report.Samples != 1 || report.Error == nil {
		t.Fatalf("report after failure = %+v", report)
	}
}

func TestClassifyH264DTSError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		message string
		want    H264DTSErrorCode
	}{
		{"too many reordered frames (49)", H264DTSErrorTooManyReorderedFrames},
		{"DTS is not monotonically increasing, was 10, now is 9", H264DTSErrorNonMonotonic},
		{"DTS is greater than PTS", H264DTSErrorAfterPTS},
		{"pic_order_cnt_type = 1 is not supported yet", H264DTSErrorUnsupportedPOC},
		{"SPS not received yet", H264DTSErrorMissingSPS},
		{"invalid SPS: invalid value", H264DTSErrorInvalidSPS},
		{"unexpected", H264DTSErrorOther},
	}
	for _, test := range tests {
		if got := classifyH264DTSError(test.message); got != test.want {
			t.Fatalf("classifyH264DTSError(%q) = %q, want %q", test.message, got, test.want)
		}
	}
}

func TestSaturatingDifference(t *testing.T) {
	t.Parallel()

	tests := []struct {
		left  int64
		right int64
		want  int64
	}{
		{left: 5, right: 3, want: 2},
		{left: math.MinInt64, right: 1, want: math.MinInt64},
		{left: math.MaxInt64, right: -1, want: math.MaxInt64},
	}
	for _, test := range tests {
		if got := saturatingDifference(test.left, test.right); got != test.want {
			t.Fatalf("saturatingDifference(%d, %d) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}

func TestAnalyzeTrackIncludesH264DTSCheck(t *testing.T) {
	t.Parallel()

	report := analyzeTrack(&trackSamples{
		track: Track{ID: 1, MediaType: MediaTypeVideo, Codec: CodecH264},
		samples: []Sample{{
			TrackID:        1,
			h264DTSChecked: true,
			h264DTS: H264DTSReport{
				Samples: 17,
				Error: &H264DTSError{
					At:      2 * time.Second,
					Code:    H264DTSErrorTooManyReorderedFrames,
					Message: "too many reordered frames (49)",
				},
			},
		}},
	})

	if report.H264 == nil || report.H264.DTS.Samples != 17 || report.H264.DTS.Error == nil {
		t.Fatalf("H264 report = %+v", report.H264)
	}
}

var h264DTSFirstAccessUnit = [][]byte{
	{
		0x67, 0x64, 0x00, 0x28, 0xac, 0xd9, 0x40, 0x78,
		0x02, 0x27, 0xe5, 0x84, 0x00, 0x00, 0x03, 0x00,
		0x04, 0x00, 0x00, 0x03, 0x00, 0xf0, 0x3c, 0x60,
		0xc6, 0x58,
	},
	{0x68, 0xce, 0x3c, 0x80},
	{0x65, 0x88, 0x84, 0x00, 0x33, 0xff},
}
