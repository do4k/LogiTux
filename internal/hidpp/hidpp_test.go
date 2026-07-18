package hidpp

import (
	"io"
	"sync"
	"testing"
	"time"
)

// fakeHandle simulates a hidraw device for testing: writes can trigger a
// canned response (via responder), and pushReport lets a test inject an
// unsolicited notification.
type fakeHandle struct {
	mu        sync.Mutex
	respQueue [][]byte
	written   [][]byte
	responder func(request []byte) []byte

	notify chan struct{}
	closed chan struct{}
	once   sync.Once
}

func newFakeHandle() *fakeHandle {
	return &fakeHandle{
		notify: make(chan struct{}, 1),
		closed: make(chan struct{}),
	}
}

func (f *fakeHandle) Write(data []byte) (int, error) {
	cp := append([]byte(nil), data...)
	f.mu.Lock()
	f.written = append(f.written, cp)
	var resp []byte
	if f.responder != nil {
		resp = f.responder(cp)
	}
	f.mu.Unlock()
	if resp != nil {
		f.pushReport(resp)
	}
	return len(data), nil
}

func (f *fakeHandle) pushReport(data []byte) {
	f.mu.Lock()
	f.respQueue = append(f.respQueue, data)
	f.mu.Unlock()
	select {
	case f.notify <- struct{}{}:
	default:
	}
}

func (f *fakeHandle) Read(buf []byte) (int, error) {
	for {
		f.mu.Lock()
		if len(f.respQueue) > 0 {
			data := f.respQueue[0]
			f.respQueue = f.respQueue[1:]
			f.mu.Unlock()
			return copy(buf, data), nil
		}
		f.mu.Unlock()

		select {
		case <-f.notify:
			continue
		case <-f.closed:
			return 0, io.EOF
		}
	}
}

func (f *fakeHandle) Close() error {
	f.once.Do(func() { close(f.closed) })
	return nil
}

// echoResponder builds a responder that answers any request addressed to
// (deviceIndex, featureIndex) with the given response parameter bytes,
// echoing the request's function/software-ID byte as HID++ requires.
func echoResponder(deviceIndex, featureIndex byte, respParams []byte) func([]byte) []byte {
	return func(req []byte) []byte {
		if req[1] != deviceIndex || req[2] != featureIndex {
			return nil
		}
		resp := make([]byte, longReportLen)
		resp[0] = reportIDLong
		resp[1] = req[1]
		resp[2] = req[2]
		resp[3] = req[3]
		copy(resp[4:], respParams)
		return resp
	}
}

func TestCallDeliversResponse(t *testing.T) {
	h := newFakeHandle()
	h.responder = echoResponder(0x01, 0x05, []byte{0xaa, 0xbb})
	c := Open(h)
	defer c.Close()

	resp, err := c.Call(0x01, 0x05, 0x20, []byte{0x01})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp[0] != 0xaa || resp[1] != 0xbb {
		t.Errorf("unexpected response params: % x", resp)
	}

	if len(h.written) != 1 {
		t.Fatalf("expected 1 write, got %d", len(h.written))
	}
	req := h.written[0]
	if req[0] != reportIDLong || req[1] != 0x01 || req[2] != 0x05 {
		t.Errorf("unexpected request header: % x", req)
	}
	if req[3]&0xf0 != 0x20 {
		t.Errorf("expected function 0x20 in high nibble, got % x", req[3])
	}
	if req[3]&0x0f == 0 {
		t.Errorf("expected a non-zero software ID in the low nibble, got % x", req[3])
	}
}

func TestCallReceivesErrorResponse(t *testing.T) {
	h := newFakeHandle()
	h.responder = func(req []byte) []byte {
		resp := make([]byte, 7)
		resp[0] = reportIDShort
		resp[1] = req[1]
		resp[2] = 0xff // error sentinel
		resp[3] = req[2]
		resp[4] = req[3]
		resp[5] = 0x06 // e.g. HIDPP_ERROR_INVALID_PARAM_VALUE
		return resp
	}
	c := Open(h)
	defer c.Close()

	_, err := c.Call(0x01, 0x05, 0x30, nil)
	if err == nil {
		t.Fatal("expected an error")
	}
	hidppErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *hidpp.Error, got %T: %v", err, err)
	}
	if hidppErr.Code != 0x06 || hidppErr.FeatureIndex != 0x05 || hidppErr.Function != 0x30 {
		t.Errorf("unexpected error fields: %+v", hidppErr)
	}
}

func TestCallTimesOutWithoutResponse(t *testing.T) {
	h := newFakeHandle() // no responder: nothing ever answers
	c := Open(h)
	c.SetTimeout(50 * time.Millisecond)
	defer c.Close()

	_, err := c.Call(0x01, 0x05, 0x10, nil)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
}

func TestUnmatchedReportBecomesNotification(t *testing.T) {
	h := newFakeHandle()
	c := Open(h)
	defer c.Close()

	unsolicited := make([]byte, 7)
	unsolicited[0] = reportIDShort
	unsolicited[1] = 0x01
	unsolicited[2] = 0x05
	unsolicited[3] = 0x00 // function 0, software ID 0: never used by a real Call (IDs start at 1)
	h.pushReport(unsolicited)

	select {
	case n := <-c.Notifications():
		if n.DeviceIndex != 0x01 {
			t.Errorf("unexpected device index: %+v", n)
		}
	case <-time.After(time.Second):
		t.Fatal("expected a notification, got none")
	}
}

func TestCloseFailsPendingCalls(t *testing.T) {
	h := newFakeHandle() // no responder: Call would otherwise hang until timeout
	c := Open(h)

	done := make(chan error, 1)
	go func() {
		_, err := c.Call(0x01, 0x05, 0x10, nil)
		done <- err
	}()

	time.Sleep(20 * time.Millisecond) // let the call register before closing
	c.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock the pending Call")
	}
}

func TestGetFeatureIndex(t *testing.T) {
	h := newFakeHandle()
	h.responder = echoResponder(0x01, RootFeatureIndex, []byte{0x0a, 0x00, 0x01})
	c := Open(h)
	defer c.Close()

	idx, ok, err := GetFeatureIndex(c, 0x01, 0x2201)
	if err != nil {
		t.Fatalf("GetFeatureIndex: %v", err)
	}
	if !ok || idx != 0x0a {
		t.Errorf("expected featureIndex=0x0a ok=true, got idx=0x%02x ok=%v", idx, ok)
	}
}

func TestGetFeatureIndexNotSupported(t *testing.T) {
	h := newFakeHandle()
	h.responder = echoResponder(0x01, RootFeatureIndex, []byte{0x00, 0x00, 0x00})
	c := Open(h)
	defer c.Close()

	_, ok, err := GetFeatureIndex(c, 0x01, 0x9999)
	if err != nil {
		t.Fatalf("GetFeatureIndex: %v", err)
	}
	if ok {
		t.Error("expected ok=false for an unsupported feature")
	}
}

func TestPing(t *testing.T) {
	h := newFakeHandle()
	h.responder = echoResponder(0x01, RootFeatureIndex, []byte{0x04, 0x02})
	c := Open(h)
	defer c.Close()

	major, minor, err := Ping(c, 0x01)
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if major != 0x04 || minor != 0x02 {
		t.Errorf("Ping() = (%d, %d), want (4, 2)", major, minor)
	}
}
