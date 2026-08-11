package mediaprobe

import (
	"context"
	"fmt"

	srt "github.com/datarhei/gosrt"
)

type srtDialResult struct {
	connection srt.Conn
	err        error
}

func openSRT(ctx context.Context, rawURL string) (*liveStream, error) {
	config := srt.DefaultConfig()
	address, err := config.UnmarshalURL(rawURL)
	if err != nil {
		return nil, ErrInvalidURL
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("validate SRT options: %w", err)
	}

	result := make(chan srtDialResult, 1)
	go func() {
		connection, dialErr := srt.Dial("srt", address, config)
		result <- srtDialResult{connection: connection, err: dialErr}
	}()

	var connection srt.Conn
	select {
	case dialResult := <-result:
		if dialResult.err != nil {
			return nil, dialResult.err
		}
		connection = dialResult.connection
	case <-ctx.Done():
		go func() {
			dialResult := <-result
			if dialResult.connection != nil {
				_ = dialResult.connection.Close()
			}
		}()
		return nil, ctx.Err()
	}

	stream := newLiveStream(ctx)
	stream.closeFunc = func() { _ = connection.Close() }
	go runMPEGTSReader(stream, connection, "SRT")
	return stream, nil
}
