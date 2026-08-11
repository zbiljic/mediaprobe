package mediaprobe_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/zbiljic/mediaprobe"
)

func TestProbeURLRTSP(t *testing.T) {
	t.Parallel()

	serverURL, writeDone := newRTSPTestServer(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	report, err := mediaprobe.ProbeURL(
		ctx,
		serverURL,
		mediaprobe.WithObservationDuration(60*time.Millisecond),
	)
	requireNoError(t, err)
	requireEqual(t, report.IsComplete, true)
	requireEqual(t, len(report.Tracks), 1)
	requireEqual(t, report.Tracks[0].Codec, mediaprobe.CodecH264)
	requireEqual(t, report.Tracks[0].MediaType, mediaprobe.MediaTypeVideo)
	requireEqual(t, report.Tracks[0].ClockRate, 90_000)
	requireEqual(t, report.Tracks[0].SampleCount >= 3, true)
	if report.Tracks[0].H264 == nil {
		t.Fatal("H264 report is nil")
	}
	requireEqual(t, report.Tracks[0].H264.DTS.Samples >= 3, true)
	if report.Tracks[0].H264.DTS.Error != nil {
		t.Fatalf("H264 DTS error = %+v", report.Tracks[0].H264.DTS.Error)
	}

	select {
	case err := <-writeDone:
		requireNoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("RTSP test server did not finish writing")
	}
}

func TestProbeURLRedactsRTSPFailure(t *testing.T) {
	t.Parallel()

	const (
		username = "stream-user"
		password = "stream-password"
		secret   = "private-signed-token"
	)
	for _, scheme := range []string{"rtsp", "rtsps"} {
		t.Run(scheme, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			requireNoError(t, err)
			address := listener.Addr().String()
			requireNoError(t, listener.Close())

			_, err = mediaprobe.ProbeURL(
				t.Context(),
				scheme+"://"+username+":"+password+"@"+address+"/live?token="+secret,
			)
			if err == nil {
				t.Fatal("ProbeURL() error = nil, want RTSP failure")
			}
			if errors.Is(err, mediaprobe.ErrUnsupportedScheme) {
				t.Fatalf("%s was treated as unsupported: %v", scheme, err)
			}
			for _, sensitive := range []string{username, password, secret, address} {
				if strings.Contains(err.Error(), sensitive) {
					t.Fatalf("error contains sensitive value %q: %v", sensitive, err)
				}
			}
		})
	}
}

func TestProbeURLHonorsContextWhileWaitingForRTSP(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	requireNoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- connection
		}
	}()

	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, probeErr := mediaprobe.ProbeURL(ctx, "rtsp://"+listener.Addr().String()+"/live")
		result <- probeErr
	}()

	var connection net.Conn
	select {
	case connection = <-accepted:
		t.Cleanup(func() { _ = connection.Close() })
	case <-time.After(5 * time.Second):
		t.Fatal("RTSP connection did not start")
	}
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ProbeURL() error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ProbeURL did not stop after context cancellation")
	}
}

type rtspTestServerHandler struct {
	stream *gortsplib.ServerStream
	played chan struct{}
	once   sync.Once
}

func (h *rtspTestServerHandler) OnDescribe(
	*gortsplib.ServerHandlerOnDescribeCtx,
) (*base.Response, *gortsplib.ServerStream, error) {
	return &base.Response{StatusCode: base.StatusOK}, h.stream, nil
}

func (h *rtspTestServerHandler) OnSetup(
	*gortsplib.ServerHandlerOnSetupCtx,
) (*base.Response, *gortsplib.ServerStream, error) {
	return &base.Response{StatusCode: base.StatusOK}, h.stream, nil
}

func (h *rtspTestServerHandler) OnPlay(
	*gortsplib.ServerHandlerOnPlayCtx,
) (*base.Response, error) {
	h.once.Do(func() { close(h.played) })
	return &base.Response{StatusCode: base.StatusOK}, nil
}

func newRTSPTestServer(t *testing.T) (string, <-chan error) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	requireNoError(t, err)
	handler := &rtspTestServerHandler{played: make(chan struct{})}
	server := &gortsplib.Server{
		RTSPAddress: "unused",
		Handler:     handler,
		Listen: func(string, string) (net.Listener, error) {
			return listener, nil
		},
	}
	requireNoError(t, server.Start())

	h264Format := &format.H264{
		PayloadTyp:        96,
		SPS:               h264FirstAccessUnit[0],
		PPS:               h264FirstAccessUnit[1],
		PacketizationMode: 1,
	}
	media := &description.Media{
		Type:    description.MediaTypeVideo,
		Formats: []format.Format{h264Format},
	}
	stream := &gortsplib.ServerStream{
		Server: server,
		Desc:   &description.Session{Medias: []*description.Media{media}},
	}
	requireNoError(t, stream.Initialize())
	handler.stream = stream
	t.Cleanup(func() {
		stream.Close()
		server.Close()
	})

	writeDone := make(chan error, 1)
	go func() {
		<-handler.played
		time.Sleep(50 * time.Millisecond)

		encoder, encodeErr := h264Format.CreateEncoder()
		if encodeErr != nil {
			writeDone <- encodeErr
			return
		}
		accessUnits := [][][]byte{
			h264FirstAccessUnit,
			{{0x41, 0x9a, 0x21, 0x6c, 0x45, 0xff}},
			{{0x41, 0x9a, 0x42, 0x3c, 0x21, 0x93}},
			{{0x41, 0x9a, 0x63, 0x49, 0xe1, 0x0f}},
		}
		for index, accessUnit := range accessUnits {
			packets, encodeErr := encoder.Encode(accessUnit)
			if encodeErr != nil {
				writeDone <- encodeErr
				return
			}
			for _, packet := range packets {
				packet.Timestamp = uint32(index * 3_000)
				if writeErr := stream.WritePacketRTP(media, packet); writeErr != nil {
					writeDone <- writeErr
					return
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		writeDone <- nil
	}()

	return "rtsp://" + listener.Addr().String() + "/live", writeDone
}
