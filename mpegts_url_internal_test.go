package mediaprobe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestProbeURLMPEGTS(t *testing.T) {
	t.Parallel()

	for _, scheme := range []string{"tcp", "udp"} {
		t.Run(scheme, func(t *testing.T) {
			payload := videoMPEGTSWithDTS(t)
			var (
				serverURL  string
				serverDone <-chan error
			)
			switch scheme {
			case "tcp":
				serverURL, serverDone = newTCPMPEGTSTestServer(t, payload)
			case "udp":
				serverURL, serverDone = newUDPMPEGTSTestSender(t, payload)
			}

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			report, err := ProbeURL(ctx, serverURL, WithObservationDuration(60*time.Millisecond))
			if err != nil {
				t.Fatalf("ProbeURL() error = %v", err)
			}
			if !report.IsComplete || len(report.Tracks) != 1 {
				t.Fatalf("report = %+v", report)
			}
			track := report.Tracks[0]
			if track.Codec != CodecH264 || track.MediaType != MediaTypeVideo || track.ClockRate != 90_000 ||
				track.SampleCount < 3 || track.H264 == nil || track.H264.DTS.Samples < 3 {
				t.Fatalf("track report = %+v", track)
			}
			if track.H264.DTS.Error != nil {
				t.Fatalf("H264 DTS error = %+v", track.H264.DTS.Error)
			}

			select {
			case serverErr := <-serverDone:
				if serverErr != nil {
					t.Fatalf("%s test server error = %v", scheme, serverErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s test server did not stop", scheme)
			}
		})
	}
}

func TestProbeURLRedactsMPEGTSFailure(t *testing.T) {
	t.Parallel()

	const (
		username = "stream-user"
		password = "stream-password"
		secret   = "private-source"
	)

	t.Run("tcp", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen TCP: %v", err)
		}
		address := listener.Addr().String()
		if err := listener.Close(); err != nil {
			t.Fatalf("close TCP listener: %v", err)
		}

		_, err = ProbeURL(t.Context(), "tcp://"+username+":"+password+"@"+address+"/live?token="+secret)
		assertRedactedMPEGTSFailure(t, err, username, password, secret, address)
	})

	t.Run("udp", func(t *testing.T) {
		const address = "127.0.0.1:0"
		_, err := ProbeURL(t.Context(), "udp://"+address+"?source="+secret)
		assertRedactedMPEGTSFailure(t, err, secret, address)
	})
}

func TestProbeURLHonorsContextWhileWaitingForMPEGTS(t *testing.T) {
	t.Parallel()

	t.Run("tcp", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen TCP: %v", err)
		}
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
			_, probeErr := ProbeURL(ctx, "tcp://"+listener.Addr().String())
			result <- probeErr
		}()

		var connection net.Conn
		select {
		case connection = <-accepted:
			t.Cleanup(func() { _ = connection.Close() })
		case <-time.After(5 * time.Second):
			t.Fatal("TCP connection did not start")
		}
		cancel()
		assertContextCanceled(t, result)
	})

	t.Run("udp", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		result := make(chan error, 1)
		go func() {
			_, probeErr := ProbeURL(ctx, "udp://127.0.0.1:0")
			result <- probeErr
		}()
		cancel()
		assertContextCanceled(t, result)
	})
}

func TestUDPStreamReaderPreservesCompleteDatagrams(t *testing.T) {
	t.Parallel()

	receiver, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	t.Cleanup(func() { _ = receiver.Close() })
	if err := receiver.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set UDP read deadline: %v", err)
	}

	sender, err := net.DialUDP("udp", nil, receiver.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatalf("dial UDP: %v", err)
	}
	t.Cleanup(func() { _ = sender.Close() })

	payload := bytes.Repeat([]byte{0x47}, 7*188)
	if _, err := sender.Write(payload); err != nil {
		t.Fatalf("write UDP datagram: %v", err)
	}

	reader := &udpStreamReader{
		connection: receiver,
		datagram:   make([]byte, maximumUDPDatagramSize),
	}
	got := make([]byte, len(payload))
	for offset := 0; offset < len(got); offset += 188 {
		read, err := io.ReadFull(reader, got[offset:offset+188])
		if err != nil {
			t.Fatalf("read UDP stream: %v", err)
		}
		if read != 188 {
			t.Fatalf("read %d bytes, want 188", read)
		}
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("UDP stream reader truncated or changed the datagram")
	}
}

func assertRedactedMPEGTSFailure(t *testing.T, err error, sensitive ...string) {
	t.Helper()

	if err == nil {
		t.Fatal("ProbeURL() error = nil, want MPEG-TS failure")
	}
	if errors.Is(err, ErrUnsupportedScheme) {
		t.Fatalf("MPEG-TS transport was treated as unsupported: %v", err)
	}
	for _, value := range sensitive {
		if strings.Contains(err.Error(), value) {
			t.Fatalf("error contains sensitive value %q: %v", value, err)
		}
	}
}

func assertContextCanceled(t *testing.T, result <-chan error) {
	t.Helper()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ProbeURL() error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ProbeURL did not stop after context cancellation")
	}
}

func newTCPMPEGTSTestServer(t *testing.T, payload []byte) (string, <-chan error) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen TCP: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	done := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		defer func() { _ = connection.Close() }()

		if _, writeErr := io.Copy(connection, bytes.NewReader(payload)); writeErr != nil {
			done <- fmt.Errorf("write TCP MPEG-TS: %w", writeErr)
			return
		}
		buffer := make([]byte, 1024)
		for {
			if _, readErr := connection.Read(buffer); readErr != nil {
				done <- nil
				return
			}
		}
	}()

	return "tcp://" + listener.Addr().String(), done
}

func newUDPMPEGTSTestSender(t *testing.T, payload []byte) (string, <-chan error) {
	t.Helper()

	reserved, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("reserve UDP address: %v", err)
	}
	address := reserved.LocalAddr().(*net.UDPAddr)
	if err := reserved.Close(); err != nil {
		t.Fatalf("release UDP address: %v", err)
	}

	sender, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatalf("listen UDP sender: %v", err)
	}
	t.Cleanup(func() { _ = sender.Close() })

	done := make(chan error, 1)
	go func() {
		for attempt := 0; attempt < 20; attempt++ {
			// Keep the opening keyframe with the tables if the receiver misses an earlier send.
			if _, writeErr := sender.WriteToUDP(payload, address); writeErr != nil {
				done <- fmt.Errorf("write UDP MPEG-TS: %w", writeErr)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		done <- nil
	}()

	return "udp://" + address.String() + "?source=127.0.0.1", done
}
