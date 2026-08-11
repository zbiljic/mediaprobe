package mediaprobe

import (
	"bytes"
	"strings"

	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
)

type h264DTSChecker struct {
	clockRate int
	sps       []byte
	pps       []byte
	extractor h264.DTSExtractor

	firstPTS    int64
	firstPTSSet bool
	started     bool
	failed      bool
	report      H264DTSReport
}

func newH264DTSChecker(clockRate int, sps, pps []byte) *h264DTSChecker {
	return &h264DTSChecker{
		clockRate: clockRate,
		sps:       bytes.Clone(sps),
		pps:       bytes.Clone(pps),
	}
}

func (c *h264DTSChecker) observe(pts int64, accessUnit [][]byte) H264DTSReport {
	if !c.firstPTSSet {
		c.firstPTS = pts
		c.firstPTSSet = true
	}

	if c.failed {
		return c.report
	}

	accessUnit, randomAccess := c.remux(accessUnit)
	if len(accessUnit) == 0 {
		return c.report
	}
	if !c.started {
		if !randomAccess {
			return c.report
		}
		c.extractor.Initialize()
		c.started = true
	}

	c.report.Samples++
	_, err := c.extractor.Extract(accessUnit, pts)
	if err != nil {
		c.failed = true
		c.report.Error = &H264DTSError{
			At:      ticksToDuration(saturatingDifference(pts, c.firstPTS), c.clockRate),
			Code:    classifyH264DTSError(err.Error()),
			Message: err.Error(),
		}
	}

	return c.report
}

// remux mirrors the H.264 normalization performed by MediaMTX streams before
// recorder readers receive access units: parameter sets and AUDs are removed,
// while the latest parameter sets are prepended to random-access units.
func (c *h264DTSChecker) remux(accessUnit [][]byte) ([][]byte, bool) {
	randomAccess := false
	requiresFiltering := false
	for _, nalu := range accessUnit {
		if len(nalu) == 0 {
			requiresFiltering = true
			continue
		}

		switch h264.NALUType(nalu[0] & 0x1F) {
		case h264.NALUTypeSPS:
			c.sps = bytes.Clone(nalu)
			requiresFiltering = true
		case h264.NALUTypePPS:
			c.pps = bytes.Clone(nalu)
			requiresFiltering = true
		case h264.NALUTypeAccessUnitDelimiter:
			requiresFiltering = true
		case h264.NALUTypeIDR:
			randomAccess = true
		}
	}

	if !randomAccess && !requiresFiltering {
		return accessUnit, false
	}

	filtered := make([][]byte, 0, len(accessUnit)+2)
	if randomAccess && c.sps != nil && c.pps != nil {
		filtered = append(filtered, c.sps, c.pps)
	}
	for _, nalu := range accessUnit {
		if len(nalu) == 0 {
			continue
		}
		switch h264.NALUType(nalu[0] & 0x1F) {
		case h264.NALUTypeSPS, h264.NALUTypePPS, h264.NALUTypeAccessUnitDelimiter:
			continue
		default:
			filtered = append(filtered, nalu)
		}
	}

	return filtered, randomAccess
}

func classifyH264DTSError(message string) H264DTSErrorCode {
	switch {
	case strings.HasPrefix(message, "too many reordered frames"):
		return H264DTSErrorTooManyReorderedFrames
	case strings.HasPrefix(message, "DTS is not monotonically increasing"):
		return H264DTSErrorNonMonotonic
	case message == "DTS is greater than PTS":
		return H264DTSErrorAfterPTS
	case strings.HasPrefix(message, "pic_order_cnt_type = 1"):
		return H264DTSErrorUnsupportedPOC
	case message == "SPS not received yet":
		return H264DTSErrorMissingSPS
	case strings.HasPrefix(message, "invalid SPS"):
		return H264DTSErrorInvalidSPS
	default:
		return H264DTSErrorOther
	}
}

func saturatingDifference(left, right int64) int64 {
	const (
		maximum = int64(^uint64(0) >> 1)
		minimum = -maximum - 1
	)
	if right > 0 && left < minimum+right {
		return minimum
	}
	if right < 0 && left > maximum+right {
		return maximum
	}
	return left - right
}
