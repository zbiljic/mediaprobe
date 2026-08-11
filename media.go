package mediaprobe

import (
	"fmt"
	"time"
)

// Codec identifies a media codec. It is intentionally open-ended so adapters
// can report codecs added by their underlying media libraries.
type Codec string

const (
	CodecAC3        Codec = "ac3"
	CodecAAC        Codec = "aac"
	CodecAV1        Codec = "av1"
	CodecEAC3       Codec = "eac3"
	CodecFLAC       Codec = "flac"
	CodecG711       Codec = "g711"
	CodecH264       Codec = "h264"
	CodecH265       Codec = "h265"
	CodecJPEG       Codec = "jpeg"
	CodecKLV        Codec = "klv"
	CodecLPCM       Codec = "lpcm"
	CodecMPEG1Audio Codec = "mpeg1audio"
	CodecMPEG1Video Codec = "mpeg1video"
	CodecMPEG4Audio Codec = "mpeg4audio"
	CodecMPEG4Video Codec = "mpeg4video"
	CodecOpus       Codec = "opus"
	CodecVP8        Codec = "vp8"
	CodecVP9        Codec = "vp9"
)

// MediaType identifies the kind of samples carried by a track.
type MediaType string

const (
	MediaTypeUnknown MediaType = "unknown"
	MediaTypeVideo   MediaType = "video"
	MediaTypeAudio   MediaType = "audio"
	MediaTypeData    MediaType = "data"
)

// Rate is an exact rational rate, such as 30000/1001 frames per second.
type Rate struct {
	Numerator   uint32 `json:"numerator"`
	Denominator uint32 `json:"denominator"`
}

// IsValid reports whether the rate has a non-zero numerator and denominator.
func (r Rate) IsValid() bool {
	return r.Numerator != 0 && r.Denominator != 0
}

// Float64 returns the decimal representation of the rate.
func (r Rate) Float64() float64 {
	if !r.IsValid() {
		return 0
	}

	return float64(r.Numerator) / float64(r.Denominator)
}

// String returns the rate in numerator/denominator form.
func (r Rate) String() string {
	if !r.IsValid() {
		return ""
	}

	return fmt.Sprintf("%d/%d", r.Numerator, r.Denominator)
}

// Track describes a logical media track. Protocol-specific codec data remains
// owned by the source implementation.
type Track struct {
	ID                int       `json:"id"`
	MediaType         MediaType `json:"type"`
	Codec             Codec     `json:"codec"`
	ClockRate         int       `json:"clock_rate,omitempty"`
	Width             int       `json:"width,omitempty"`
	Height            int       `json:"height,omitempty"`
	SampleRate        int       `json:"sample_rate,omitempty"`
	Channels          int       `json:"channels,omitempty"`
	DeclaredFrameRate Rate      `json:"declared_frame_rate,omitzero"`
}

// Sample describes the timing of one compressed media sample. When a transport
// does not distinguish PTS and DTS, its adapter should set both to the same value.
type Sample struct {
	TrackID    int
	PTS        time.Duration
	DTS        time.Duration
	Duration   time.Duration
	IsKeyFrame bool
	Size       int
}
