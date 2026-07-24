//go:build linux

// Package v4l2 provides the minimal slice of the Video4Linux2 API that
// LogiTux needs to adjust webcams and show a live preview: querying and
// setting *controls* (zoom, pan/tilt, focus, exposure, image tuning) via
// the VIDIOC_QUERYCAP / QUERYCTRL / G_CTRL / S_CTRL ioctls, and capturing
// frames via VIDIOC_S_FMT / REQBUFS / QUERYBUF / QBUF / DQBUF / STREAMON
// / STREAMOFF with mmap'd (V4L2_MEMORY_MMAP) buffers — mirroring
// linux/videodev2.h. Like internal/hid and internal/uinput, this is pure
// Go (x/sys/unix) — no libv4l, no cgo.
package v4l2

import (
	"encoding/binary"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Control IDs, from linux/v4l2-controls.h. User class is 0x00980900 + n;
// camera class is 0x009a0900 + n.
const (
	CtrlBrightness = 0x00980900 + 0
	CtrlContrast   = 0x00980900 + 1
	CtrlSaturation = 0x00980900 + 2
	CtrlSharpness  = 0x00980900 + 27

	CtrlExposureAuto     = 0x009a0900 + 1
	CtrlExposureAbsolute = 0x009a0900 + 2
	CtrlPanAbsolute      = 0x009a0900 + 8
	CtrlTiltAbsolute     = 0x009a0900 + 9
	CtrlFocusAbsolute    = 0x009a0900 + 10
	CtrlFocusAuto        = 0x009a0900 + 12
	CtrlZoomAbsolute     = 0x009a0900 + 13
)

// v4l2_exposure_auto_type menu values for CtrlExposureAuto. UVC webcams
// implement exactly these two: fully manual, and auto ("aperture
// priority", since a webcam's aperture is fixed).
const (
	ExposureManual       = 1
	ExposureAperturePrio = 3
)

// Capability flags (v4l2_capability.device_caps). A UVC webcam exposes
// several /dev/videoN nodes; only the one with CapVideoCapture set is
// the camera itself (the others are e.g. metadata nodes).
const CapVideoCapture = 0x00000001

// Control flags (v4l2_queryctrl.flags).
const flagDisabled = 0x0001

// v4l2_buf_type and v4l2_memory values. LogiTux only ever captures from
// the (non-multiplanar) video-capture queue using mmap'd buffers.
const (
	bufTypeVideoCapture = 1
	memoryMMAP          = 1
)

// Pixel formats (V4L2_PIX_FMT_*), each a 4-character-code packed
// little-endian the way V4L2_PIX_FMT_FOURCC assembles it in C.
func fourCC(a, b, c, d byte) uint32 {
	return uint32(a) | uint32(b)<<8 | uint32(c)<<16 | uint32(d)<<24
}

var (
	// PixFmtMJPEG is what the preview asks for first: compressed, so it's
	// cheap on both the USB link and (decoded with the stdlib's
	// image/jpeg) the CPU.
	PixFmtMJPEG = fourCC('M', 'J', 'P', 'G')
	// PixFmtYUYV is the fallback for hardware/resolutions that don't
	// offer MJPEG; it needs manual YCbCr conversion (see the webcam
	// plugin's decodeFrame).
	PixFmtYUYV = fourCC('Y', 'U', 'Y', 'V')
)

// ioctl request-code construction, mirroring the _IOC macro in
// linux/ioctl.h, so these values are derived rather than copied magic
// numbers. 'V' (0x56) is the videodev2 ioctl type.
const (
	iocWrite = 1
	iocRead  = 2
)

func vidioc(dir, nr, size uintptr) uintptr {
	return dir<<30 | size<<16 | 'V'<<8 | nr
}

// Sizes of the marshaled structs below, fixed by the kernel ABI. The
// streaming structs (v4l2_format, v4l2_requestbuffers, v4l2_buffer) are
// only correct for 64-bit builds — their layout depends on the size of
// a C `long`/pointer (see sizeofBuffer) — which matches every platform
// LogiTux ships on.
const (
	sizeofCapability = 104
	sizeofQueryctrl  = 68
	sizeofControl    = 8
	// sizeofFormat is sizeof(struct v4l2_format) on 64-bit: 4 bytes for
	// the leading type field, then 4 bytes of padding before the fmt
	// union (which needs 8-byte alignment, since some of its non-pix
	// variants carry a pointer), then the 200-byte union itself.
	sizeofFormat         = 208
	sizeofRequestBuffers = 20
	// sizeofBuffer is sizeof(struct v4l2_buffer) on 64-bit Linux: the
	// struct's timeval/timecode/union members put it at 88 bytes, vs. 68
	// on 32-bit (where timeval's two longs are 4 bytes each instead of 8).
	sizeofBuffer = 88
)

var (
	vidiocQuerycap  = vidioc(iocRead, 0, sizeofCapability)
	vidiocGCtrl     = vidioc(iocRead|iocWrite, 27, sizeofControl)
	vidiocSCtrl     = vidioc(iocRead|iocWrite, 28, sizeofControl)
	vidiocQueryctrl = vidioc(iocRead|iocWrite, 36, sizeofQueryctrl)

	vidiocGFmt      = vidioc(iocRead|iocWrite, 4, sizeofFormat)
	vidiocSFmt      = vidioc(iocRead|iocWrite, 5, sizeofFormat)
	vidiocReqbufs   = vidioc(iocRead|iocWrite, 8, sizeofRequestBuffers)
	vidiocQuerybuf  = vidioc(iocRead|iocWrite, 9, sizeofBuffer)
	vidiocQbuf      = vidioc(iocRead|iocWrite, 15, sizeofBuffer)
	vidiocDqbuf     = vidioc(iocRead|iocWrite, 17, sizeofBuffer)
	vidiocStreamon  = vidioc(iocWrite, 18, 4)
	vidiocStreamoff = vidioc(iocWrite, 19, 4)
)

// ioctlFunc is swapped out by tests to fake the kernel side.
type ioctlFunc func(fd int, req uintptr, arg unsafe.Pointer) error

func realIoctl(fd int, req uintptr, arg unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(arg))
	if errno != 0 {
		return errno
	}
	return nil
}

// mmapFunc/munmapFunc are swapped out by tests, the same way ioctlFunc
// is: real mmap needs a real page-backed fd, which a test's throwaway
// temp file isn't.
type mmapFunc func(fd int, offset int64, length int) ([]byte, error)
type munmapFunc func(b []byte) error

func realMmap(fd int, offset int64, length int) ([]byte, error) {
	return unix.Mmap(fd, offset, length, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
}

func realMunmap(b []byte) error { return unix.Munmap(b) }

// ControlInfo describes one control's range as reported by the driver.
type ControlInfo struct {
	Min, Max, Step, Default int32
}

// Device is an open V4L2 device node.
type Device struct {
	f      *os.File
	ioctl  ioctlFunc
	mmap   mmapFunc
	munmap munmapFunc
}

// Open opens a /dev/videoN node for control access and capture.
func Open(path string) (*Device, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("v4l2: open %s: %w", path, err)
	}
	return &Device{f: f, ioctl: realIoctl, mmap: realMmap, munmap: realMunmap}, nil
}

func (d *Device) Close() error { return d.f.Close() }

// QueryCap returns the device's card name and device_caps flags
// (v4l2_capability layout: driver[16], card[32], bus_info[32], version
// u32, capabilities u32, device_caps u32, reserved u32[3]).
func (d *Device) QueryCap() (card string, deviceCaps uint32, err error) {
	var buf [sizeofCapability]byte
	if err := d.ioctl(int(d.f.Fd()), vidiocQuerycap, unsafe.Pointer(&buf[0])); err != nil {
		return "", 0, fmt.Errorf("v4l2: QUERYCAP %s: %w", d.f.Name(), err)
	}
	return cString(buf[16:48]), binary.LittleEndian.Uint32(buf[88:92]), nil
}

// QueryControl reports a control's range, or an error if the device
// doesn't have it (v4l2_queryctrl layout: id u32, type u32, name[32],
// minimum s32, maximum s32, step s32, default_value s32, flags u32,
// reserved u32[2]).
func (d *Device) QueryControl(id uint32) (ControlInfo, error) {
	var buf [sizeofQueryctrl]byte
	binary.LittleEndian.PutUint32(buf[0:4], id)
	if err := d.ioctl(int(d.f.Fd()), vidiocQueryctrl, unsafe.Pointer(&buf[0])); err != nil {
		return ControlInfo{}, fmt.Errorf("v4l2: QUERYCTRL %#x on %s: %w", id, d.f.Name(), err)
	}
	if binary.LittleEndian.Uint32(buf[56:60])&flagDisabled != 0 {
		return ControlInfo{}, fmt.Errorf("v4l2: control %#x on %s is disabled", id, d.f.Name())
	}
	s32 := func(b []byte) int32 { return int32(binary.LittleEndian.Uint32(b)) }
	return ControlInfo{
		Min:     s32(buf[40:44]),
		Max:     s32(buf[44:48]),
		Step:    s32(buf[48:52]),
		Default: s32(buf[52:56]),
	}, nil
}

// Get reads a control's current value (v4l2_control layout: id u32,
// value s32).
func (d *Device) Get(id uint32) (int32, error) {
	var buf [sizeofControl]byte
	binary.LittleEndian.PutUint32(buf[0:4], id)
	if err := d.ioctl(int(d.f.Fd()), vidiocGCtrl, unsafe.Pointer(&buf[0])); err != nil {
		return 0, fmt.Errorf("v4l2: G_CTRL %#x on %s: %w", id, d.f.Name(), err)
	}
	return int32(binary.LittleEndian.Uint32(buf[4:8])), nil
}

// Set writes a control's value.
func (d *Device) Set(id uint32, value int32) error {
	var buf [sizeofControl]byte
	binary.LittleEndian.PutUint32(buf[0:4], id)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(value))
	if err := d.ioctl(int(d.f.Fd()), vidiocSCtrl, unsafe.Pointer(&buf[0])); err != nil {
		return fmt.Errorf("v4l2: S_CTRL %#x=%d on %s: %w", id, value, d.f.Name(), err)
	}
	return nil
}

// Format describes a negotiated capture format — the subset of
// v4l2_pix_format that frame decoding needs.
type Format struct {
	Width, Height int
	PixelFormat   uint32
	BytesPerLine  int
	SizeImage     int
}

// parseFormat reads back a v4l2_format buffer after S_FMT/G_FMT fills it
// in (layout: type u32 @0, 4 bytes of padding so the union that follows
// lands on an 8-byte boundary, then its v4l2_pix_format union starting
// at @8: width, height, pixelformat, field, bytesperline, sizeimage,
// all u32).
func parseFormat(buf []byte) Format {
	u32 := func(b []byte) uint32 { return binary.LittleEndian.Uint32(b) }
	return Format{
		Width:        int(u32(buf[8:12])),
		Height:       int(u32(buf[12:16])),
		PixelFormat:  u32(buf[16:20]),
		BytesPerLine: int(u32(buf[24:28])),
		SizeImage:    int(u32(buf[28:32])),
	}
}

// SetFormat negotiates the capture resolution and pixel format
// (VIDIOC_S_FMT). The driver may substitute a different pixel format or
// clamp width/height to whatever it actually supports, so the returned
// Format reflects what was negotiated, not what was asked for. Some
// drivers (and V4L2 compatibility shims some desktops put in front of
// the real device, e.g. PipeWire's) refuse to renegotiate format at
// all — see GetFormat for reading whatever's already active instead.
func (d *Device) SetFormat(width, height int, pixelFormat uint32) (Format, error) {
	var buf [sizeofFormat]byte
	binary.LittleEndian.PutUint32(buf[0:4], bufTypeVideoCapture)
	binary.LittleEndian.PutUint32(buf[8:12], uint32(width))
	binary.LittleEndian.PutUint32(buf[12:16], uint32(height))
	binary.LittleEndian.PutUint32(buf[16:20], pixelFormat)
	if err := d.ioctl(int(d.f.Fd()), vidiocSFmt, unsafe.Pointer(&buf[0])); err != nil {
		return Format{}, fmt.Errorf("v4l2: S_FMT %s: %w", d.f.Name(), err)
	}
	return parseFormat(buf[:]), nil
}

// GetFormat reads the capture format currently active (VIDIOC_G_FMT),
// without asking the driver to change anything. Useful as a fallback
// when SetFormat is refused: a device already streaming a usable
// format for something else will often still report it here.
func (d *Device) GetFormat() (Format, error) {
	var buf [sizeofFormat]byte
	binary.LittleEndian.PutUint32(buf[0:4], bufTypeVideoCapture)
	if err := d.ioctl(int(d.f.Fd()), vidiocGFmt, unsafe.Pointer(&buf[0])); err != nil {
		return Format{}, fmt.Errorf("v4l2: G_FMT %s: %w", d.f.Name(), err)
	}
	return parseFormat(buf[:]), nil
}

// RequestBuffers allocates (or, given 0, releases) count MMAP capture
// buffers (VIDIOC_REQBUFS) and returns how many the driver actually
// granted, which may be fewer than asked for (v4l2_requestbuffers
// layout: count u32, type u32, memory u32, capabilities u32, flags u8,
// reserved u8[3]).
func (d *Device) RequestBuffers(count int) (int, error) {
	var buf [sizeofRequestBuffers]byte
	binary.LittleEndian.PutUint32(buf[0:4], uint32(count))
	binary.LittleEndian.PutUint32(buf[4:8], bufTypeVideoCapture)
	binary.LittleEndian.PutUint32(buf[8:12], memoryMMAP)
	if err := d.ioctl(int(d.f.Fd()), vidiocReqbufs, unsafe.Pointer(&buf[0])); err != nil {
		return 0, fmt.Errorf("v4l2: REQBUFS %s: %w", d.f.Name(), err)
	}
	return int(binary.LittleEndian.Uint32(buf[0:4])), nil
}

// bufferRequest fills the common index/type/memory prefix of a
// v4l2_buffer (layout: index u32 @0, type u32 @4, ..., memory u32 @60)
// that QUERYBUF/QBUF/DQBUF all share.
func bufferRequest(index int) [sizeofBuffer]byte {
	var buf [sizeofBuffer]byte
	binary.LittleEndian.PutUint32(buf[0:4], uint32(index))
	binary.LittleEndian.PutUint32(buf[4:8], bufTypeVideoCapture)
	binary.LittleEndian.PutUint32(buf[60:64], memoryMMAP)
	return buf
}

// MapBuffer queries buffer index's kernel-side offset/length
// (VIDIOC_QUERYBUF) and mmaps it into the process. For MMAP-memory
// buffers those live in v4l2_buffer's union at offset 64 (a plain u32
// offset, since this is the single-planar API): m.offset @64, length @72.
func (d *Device) MapBuffer(index int) ([]byte, error) {
	buf := bufferRequest(index)
	if err := d.ioctl(int(d.f.Fd()), vidiocQuerybuf, unsafe.Pointer(&buf[0])); err != nil {
		return nil, fmt.Errorf("v4l2: QUERYBUF %d on %s: %w", index, d.f.Name(), err)
	}
	offset := binary.LittleEndian.Uint32(buf[64:68])
	length := binary.LittleEndian.Uint32(buf[72:76])
	mem, err := d.mmap(int(d.f.Fd()), int64(offset), int(length))
	if err != nil {
		return nil, fmt.Errorf("v4l2: mmap buffer %d on %s: %w", index, d.f.Name(), err)
	}
	return mem, nil
}

// UnmapBuffer releases a buffer previously returned by MapBuffer.
func (d *Device) UnmapBuffer(mem []byte) error {
	if err := d.munmap(mem); err != nil {
		return fmt.Errorf("v4l2: munmap %s: %w", d.f.Name(), err)
	}
	return nil
}

// QueueBuffer hands buffer index back to the driver to be filled with
// the next captured frame (VIDIOC_QBUF).
func (d *Device) QueueBuffer(index int) error {
	buf := bufferRequest(index)
	if err := d.ioctl(int(d.f.Fd()), vidiocQbuf, unsafe.Pointer(&buf[0])); err != nil {
		return fmt.Errorf("v4l2: QBUF %d on %s: %w", index, d.f.Name(), err)
	}
	return nil
}

// DequeueBuffer blocks until a filled buffer is available (VIDIOC_DQBUF),
// returning its index and how many bytes of frame data it holds
// (bytesused, v4l2_buffer offset 8). Callers should treat an error here
// as "streaming stopped" — StreamOff unblocks a pending DQBUF on another
// goroutine exactly this way.
func (d *Device) DequeueBuffer() (index, bytesUsed int, err error) {
	var buf [sizeofBuffer]byte
	binary.LittleEndian.PutUint32(buf[4:8], bufTypeVideoCapture)
	binary.LittleEndian.PutUint32(buf[60:64], memoryMMAP)
	if err := d.ioctl(int(d.f.Fd()), vidiocDqbuf, unsafe.Pointer(&buf[0])); err != nil {
		return 0, 0, fmt.Errorf("v4l2: DQBUF %s: %w", d.f.Name(), err)
	}
	return int(binary.LittleEndian.Uint32(buf[0:4])), int(binary.LittleEndian.Uint32(buf[8:12])), nil
}

// StreamOn starts capture (VIDIOC_STREAMON); buffers must already be
// queued (see QueueBuffer) or the driver has nothing to fill.
func (d *Device) StreamOn() error {
	t := uint32(bufTypeVideoCapture)
	if err := d.ioctl(int(d.f.Fd()), vidiocStreamon, unsafe.Pointer(&t)); err != nil {
		return fmt.Errorf("v4l2: STREAMON %s: %w", d.f.Name(), err)
	}
	return nil
}

// StreamOff stops capture (VIDIOC_STREAMOFF). If another goroutine is
// blocked in DequeueBuffer, this causes that call to return an error —
// the vb2 core wakes any waiting DQBUF when the queue is torn down —
// which is how a capture loop notices it should exit.
func (d *Device) StreamOff() error {
	t := uint32(bufTypeVideoCapture)
	if err := d.ioctl(int(d.f.Fd()), vidiocStreamoff, unsafe.Pointer(&t)); err != nil {
		return fmt.Errorf("v4l2: STREAMOFF %s: %w", d.f.Name(), err)
	}
	return nil
}

func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
