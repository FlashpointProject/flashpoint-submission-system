package resumableuploadservice

import (
	"bytes"
	"io"
	"runtime"
	"testing"
)

// The upload worker copies bytes while a separate goroutine polls progress.
// Exercise real chunk transitions and verify reconstructed content under -race.
func TestFileReaderConcurrentProgress(t *testing.T) {
	rsu, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const chunks = 32
	var want []byte
	for i := 1; i <= chunks; i++ {
		chunk := bytes.Repeat([]byte{byte(i)}, 1024+i)
		want = append(want, chunk...)
		if err := rsu.PutChunk(1, "progress", i, chunk); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := rsu.NewFileReader(1, "progress", chunks)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if fraction := reader.GetFractionRead(); fraction != 0 {
		t.Fatalf("initial progress %v", fraction)
	}
	started, stop, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		last := reader.GetFractionRead()
		close(started)
		for {
			select {
			case <-stop:
				return
			default:
			}
			fraction := reader.GetFractionRead()
			if fraction < last {
				t.Errorf("progress moved backwards: %v -> %v", last, fraction)
				return
			}
			last = fraction
			runtime.Gosched()
		}
	}()
	<-started
	var got bytes.Buffer
	buf := make([]byte, 37)
	for {
		n, err := reader.Read(buf)
		got.Write(buf[:n])
		if err != nil {
			if err != io.EOF {
				t.Errorf("read: %v", err)
			}
			break
		}
		runtime.Gosched()
	}
	close(stop)
	<-done
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatal("concurrent progress polling changed reconstructed bytes")
	}
	if fraction := reader.GetFractionRead(); fraction < 1 {
		t.Fatalf("incomplete final progress %v", fraction)
	}
}
