package mediaprobe

import (
	"context"
	"errors"
	"io"
	"sync"
)

type liveStream struct {
	ctx       context.Context
	cancel    context.CancelFunc
	tracks    chan []Track
	samples   chan Sample
	failures  chan error
	closeOnce sync.Once
	closeFunc func()
}

func newLiveStream(ctx context.Context) *liveStream {
	streamCtx, cancel := context.WithCancel(ctx)
	return &liveStream{
		ctx:      streamCtx,
		cancel:   cancel,
		tracks:   make(chan []Track, 1),
		samples:  make(chan Sample),
		failures: make(chan error, 1),
	}
}

func (s *liveStream) Tracks(ctx context.Context) ([]Track, error) {
	// Track publication always precedes a terminal client error. Prefer the
	// metadata when a short VOD stream has already produced both.
	select {
	case tracks := <-s.tracks:
		return tracks, nil
	default:
	}

	select {
	case tracks := <-s.tracks:
		return tracks, nil
	case err := <-s.failures:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.ctx.Done():
		return nil, io.EOF
	}
}

func (s *liveStream) ReadSample(ctx context.Context) (Sample, error) {
	select {
	case sample := <-s.samples:
		return sample, nil
	case err := <-s.failures:
		return Sample{}, err
	case <-ctx.Done():
		return Sample{}, ctx.Err()
	case <-s.ctx.Done():
		return Sample{}, io.EOF
	}
}

func (s *liveStream) publishTracks(tracks []Track) error {
	if len(tracks) == 0 {
		return ErrNoTracks
	}

	select {
	case s.tracks <- tracks:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func (s *liveStream) publishSample(sample Sample) {
	select {
	case s.samples <- sample:
	case <-s.ctx.Done():
	}
}

func (s *liveStream) fail(err error) {
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}

	select {
	case s.failures <- err:
	default:
	}
}

func (s *liveStream) Close() {
	s.closeOnce.Do(func() {
		s.cancel()
		if s.closeFunc != nil {
			s.closeFunc()
		}
	})
}

var _ Stream = (*liveStream)(nil)
