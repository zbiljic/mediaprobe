# mediaprobe

`mediaprobe` is a Go library for bounded observation of media streams, currently focused on live sources. It reports tracks, timing, cadence, bitrate, and H.264 DTS diagnostics.

The API and report format are early and versioned; they may evolve as the library develops.

## Install

```sh
go get github.com/zbiljic/mediaprobe
```

## Usage

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/zbiljic/mediaprobe"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	report, err := mediaprobe.ProbeURL(ctx, "https://example.com/live/index.m3u8")
	if err != nil {
		fmt.Printf("probe error: %v (partial tracks: %d)\n", err, len(report.Tracks))
		return
	}

	fmt.Printf("complete: %v, tracks: %d\n", report.IsComplete, len(report.Tracks))
}
```

When probing returns an error, the returned report may still contain partial measurements.

## Supported URLs

`ProbeURL` currently supports HTTP(S) HLS, RTSP(S), RTMP(S), SRT, and raw MPEG-TS over TCP or UDP.

Finite/local-file inspection is not yet a built-in API; callers can use `ProbeStream` with their own `Stream` adapter.

## Development

Run the unit tests:

```sh
go test ./...
```

Run the full project checks:

```sh
mise run pre-commit
```

## License

Licensed under the [MIT License](LICENSE).
