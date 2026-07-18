// Package hidpp implements the transport layer of Logitech's HID++ 2.0
// protocol: the request/response feature-call protocol used by most
// Logitech peripherals (mice, keyboards, headsets, receivers) as opposed
// to the simple vendor HID commands used by Litra lights.
//
// A HID++ 2.0 report has a 4-byte header followed by parameters:
//
//	byte 0: report ID   (0x10 = 7-byte "short" report, 0x11 = 20-byte "long" report)
//	byte 1: device index (0xFF for a directly-connected device; 0x01-0x06
//	        for a device paired to a receiver)
//	byte 2: feature index (which feature table entry to call; feature index
//	        0 is always the Root feature)
//	byte 3: function | software ID
//	byte 4+: parameters
//
// byte 3 packs two fields: the function number in the high nibble and a
// caller-chosen software ID in the low nibble. Following the convention
// used throughout Logitech's own tooling and Solaar (the reference Linux
// HID++ implementation), function numbers are written and passed around
// pre-shifted into that high nibble (0x00, 0x10, 0x20, ..., 0xF0) rather
// than as small integers — Call's function parameter expects that form.
//
// The device echoes back the device index, feature index, function number,
// and software ID unchanged in its response, which is how a client matches
// a response (or error) to the call that produced it. Reports that don't
// match a pending call are unsolicited notifications (e.g. a battery-level
// push, or a diverted button press).
//
// Feature IDs (e.g. 0x2201 for Adjustable DPI) are fixed constants defined
// by Logitech, but a device's feature *index* is assigned per-device and
// must be discovered at runtime via the Root feature's GetFeature call —
// see GetFeatureIndex.
package hidpp

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"logitux/internal/hid"
)

const (
	reportIDShort byte = 0x10
	reportIDLong  byte = 0x11

	longReportLen = 20
)

// DeviceIndexDirect addresses a HID++ device connected directly (wired USB
// or Bluetooth), as opposed to one paired through a receiver.
const DeviceIndexDirect byte = 0xff

// RootFeatureIndex is fixed by the HID++ spec: the Root feature is always
// at feature index 0, so it never needs to be looked up.
const RootFeatureIndex byte = 0x00

// Error is a HID++ error response: the device rejected a call instead of
// returning a normal result. Function is in the same pre-shifted form
// (0x00, 0x10, 0x20, ...) that Call expects.
type Error struct {
	FeatureIndex byte
	Function     byte
	Code         byte
}

func (e *Error) Error() string {
	return fmt.Sprintf("hidpp: device error 0x%02x (feature index 0x%02x, function 0x%02x)", e.Code, e.FeatureIndex, e.Function)
}

// Notification is an unsolicited HID++ report: one that didn't match any
// pending Call.
type Notification struct {
	DeviceIndex byte
	Data        []byte // full report, including the report ID byte
}

type callKey struct {
	deviceIndex  byte
	featureIndex byte
	funcSwID     byte
}

type pendingCall struct {
	resp chan []byte
	err  chan error
}

// Conn is a HID++ 2.0 connection over a single hidraw handle. One Conn can
// carry traffic for several device indices at once, as when several
// peripherals are paired to the same wireless receiver.
type Conn struct {
	h hid.Handle

	mu      sync.Mutex
	timeout time.Duration
	nextSW  byte
	pending map[callKey]pendingCall
	closed  bool

	notifications chan Notification
}

// defaultCallTimeout is a var (not const) so tests can shrink it instead of
// waiting out the real timeout.
var defaultCallTimeout = 2 * time.Second

// Open starts a Conn over an already-opened hidraw handle and begins
// reading reports from it in the background. Call Close when done; that
// unblocks the background reader and fails any calls still in flight.
func Open(h hid.Handle) *Conn {
	c := &Conn{
		h:             h,
		timeout:       defaultCallTimeout,
		nextSW:        1,
		pending:       make(map[callKey]pendingCall),
		notifications: make(chan Notification, 16),
	}
	go c.readLoop()
	return c
}

// SetTimeout changes how long subsequent Calls wait for a response.
// Useful to shorten it temporarily, e.g. when probing several device
// indices behind a receiver for a live device.
func (c *Conn) SetTimeout(d time.Duration) {
	c.mu.Lock()
	c.timeout = d
	c.mu.Unlock()
}

// Notifications returns the channel of unsolicited reports. The read loop
// never blocks on this channel: if nothing is draining it, notifications
// are dropped rather than stalling response delivery.
func (c *Conn) Notifications() <-chan Notification {
	return c.notifications
}

// Close closes the underlying handle.
func (c *Conn) Close() error {
	return c.h.Close()
}

// Call invokes function (pre-shifted into the high nibble, e.g. 0x10 for
// function 1 — see the package doc) on featureIndex for deviceIndex with
// params (at most 16 bytes), and returns the response's parameter bytes.
func (c *Conn) Call(deviceIndex, featureIndex, function byte, params []byte) ([]byte, error) {
	if len(params) > longReportLen-4 {
		return nil, fmt.Errorf("hidpp: too many parameters (%d > %d)", len(params), longReportLen-4)
	}

	swID := c.allocSoftwareID()
	funcSwID := (function & 0xf0) | swID
	key := callKey{deviceIndex: deviceIndex, featureIndex: featureIndex, funcSwID: funcSwID}

	resp := make(chan []byte, 1)
	errc := make(chan error, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("hidpp: connection closed")
	}
	timeout := c.timeout
	c.pending[key] = pendingCall{resp: resp, err: errc}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
	}()

	report := make([]byte, longReportLen)
	report[0] = reportIDLong
	report[1] = deviceIndex
	report[2] = featureIndex
	report[3] = funcSwID
	copy(report[4:], params)

	if _, err := c.h.Write(report); err != nil {
		return nil, fmt.Errorf("hidpp: write: %w", err)
	}

	select {
	case data := <-resp:
		return data, nil
	case err := <-errc:
		return nil, err
	case <-time.After(timeout):
		return nil, fmt.Errorf("hidpp: timed out waiting for response (device 0x%02x, feature index 0x%02x, function 0x%x)", deviceIndex, featureIndex, function)
	}
}

func (c *Conn) allocSoftwareID() byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := c.nextSW
	c.nextSW++
	if c.nextSW > 0x0f {
		c.nextSW = 1
	}
	return id
}

// readLoop continuously reads reports from the handle, delivering each one
// to whichever Call is waiting for it, or else to the notifications
// channel. It exits (and closes the connection) when the read fails, which
// is how Close unblocks it.
func (c *Conn) readLoop() {
	buf := make([]byte, longReportLen)
	for {
		n, err := c.h.Read(buf)
		if err != nil {
			c.shutdown()
			return
		}
		if n < 4 {
			continue
		}

		reportID := buf[0]
		if reportID != reportIDShort && reportID != reportIDLong {
			continue // not a HID++ report, e.g. a plain HID report on a shared interface
		}
		deviceIndex := buf[1]

		// HID++ 2.0 error responses are always a short report with 0xFF in
		// the feature-index position.
		if reportID == reportIDShort && n >= 6 && buf[2] == 0xff {
			key := callKey{deviceIndex: deviceIndex, featureIndex: buf[3], funcSwID: buf[4]}
			c.deliverError(key, &Error{FeatureIndex: buf[3], Function: buf[4] & 0xf0, Code: buf[5]})
			continue
		}

		key := callKey{deviceIndex: deviceIndex, featureIndex: buf[2], funcSwID: buf[3]}
		data := append([]byte(nil), buf[4:n]...)
		if c.deliverResponse(key, data) {
			continue
		}

		report := append([]byte(nil), buf[:n]...)
		select {
		case c.notifications <- Notification{DeviceIndex: deviceIndex, Data: report}:
		default:
		}
	}
}

func (c *Conn) deliverResponse(key callKey, data []byte) bool {
	c.mu.Lock()
	call, ok := c.pending[key]
	if ok {
		delete(c.pending, key)
	}
	c.mu.Unlock()
	if !ok {
		return false
	}
	call.resp <- data
	return true
}

func (c *Conn) deliverError(key callKey, err error) {
	c.mu.Lock()
	call, ok := c.pending[key]
	if ok {
		delete(c.pending, key)
	}
	c.mu.Unlock()
	if ok {
		call.err <- err
	}
}

func (c *Conn) shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	for key, call := range c.pending {
		call.err <- errors.New("hidpp: connection closed")
		delete(c.pending, key)
	}
	close(c.notifications)
}
