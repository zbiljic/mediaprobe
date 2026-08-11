package mediaprobe

import "errors"

var (
	ErrNoTracks                   = errors.New("mediaprobe: stream has no tracks")
	ErrInvalidTrack               = errors.New("mediaprobe: invalid track")
	ErrInvalidSample              = errors.New("mediaprobe: invalid sample")
	ErrUnknownTrack               = errors.New("mediaprobe: sample references unknown track")
	ErrObservationIncomplete      = errors.New("mediaprobe: observation incomplete")
	ErrInvalidObservationDuration = errors.New("mediaprobe: invalid observation duration")
	ErrInvalidSampleLimit         = errors.New("mediaprobe: invalid sample limit")
	ErrSampleLimitExceeded        = errors.New("mediaprobe: sample limit exceeded")
)
