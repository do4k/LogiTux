//go:build linux

package v4l2

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"unsafe"
)

// Request codes as computed by the C _IOC macro for the videodev2
// structs on every Linux ABI Go supports — derived independently here so
// a typo in vidioc() can't cancel itself out.
func TestRequestCodes(t *testing.T) {
	cases := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"VIDIOC_QUERYCAP", vidiocQuerycap, 0x80685600},
		{"VIDIOC_G_CTRL", vidiocGCtrl, 0xc008561b},
		{"VIDIOC_S_CTRL", vidiocSCtrl, 0xc008561c},
		{"VIDIOC_QUERYCTRL", vidiocQueryctrl, 0xc0445624},
		{"VIDIOC_G_FMT", vidiocGFmt, 0xc0d05604},
		{"VIDIOC_S_FMT", vidiocSFmt, 0xc0d05605},
		{"VIDIOC_REQBUFS", vidiocReqbufs, 0xc0145608},
		{"VIDIOC_QUERYBUF", vidiocQuerybuf, 0xc0585609},
		{"VIDIOC_QBUF", vidiocQbuf, 0xc058560f},
		{"VIDIOC_DQBUF", vidiocDqbuf, 0xc0585611},
		{"VIDIOC_STREAMON", vidiocStreamon, 0x40045612},
		{"VIDIOC_STREAMOFF", vidiocStreamoff, 0x40045613},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %#x, want %#x", c.name, c.got, c.want)
		}
	}
}

// testDevice returns a Device whose ioctl is the given fake, backed by a
// throwaway real file (only its fd number is used).
func testDevice(t *testing.T, fake ioctlFunc) *Device {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "video0"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return &Device{f: f, ioctl: fake}
}

func TestQueryCap(t *testing.T) {
	d := testDevice(t, func(fd int, req uintptr, arg unsafe.Pointer) error {
		if req != vidiocQuerycap {
			t.Fatalf("unexpected request %#x", req)
		}
		buf := unsafe.Slice((*byte)(arg), sizeofCapability)
		copy(buf[16:], "C922 Pro Stream Webcam\x00")
		binary.LittleEndian.PutUint32(buf[88:92], CapVideoCapture|0x04000000)
		return nil
	})
	card, caps, err := d.QueryCap()
	if err != nil {
		t.Fatal(err)
	}
	if card != "C922 Pro Stream Webcam" {
		t.Errorf("card = %q", card)
	}
	if caps&CapVideoCapture == 0 {
		t.Errorf("caps = %#x, missing CapVideoCapture", caps)
	}
}

func TestQueryControl(t *testing.T) {
	d := testDevice(t, func(fd int, req uintptr, arg unsafe.Pointer) error {
		buf := unsafe.Slice((*byte)(arg), sizeofQueryctrl)
		if got := binary.LittleEndian.Uint32(buf[0:4]); got != CtrlZoomAbsolute {
			t.Fatalf("queried id %#x, want %#x", got, CtrlZoomAbsolute)
		}
		binary.LittleEndian.PutUint32(buf[40:44], 100) // min
		binary.LittleEndian.PutUint32(buf[44:48], 500) // max
		binary.LittleEndian.PutUint32(buf[48:52], 1)   // step
		binary.LittleEndian.PutUint32(buf[52:56], 100) // default
		binary.LittleEndian.PutUint32(buf[56:60], 0)   // flags
		return nil
	})
	info, err := d.QueryControl(CtrlZoomAbsolute)
	if err != nil {
		t.Fatal(err)
	}
	want := ControlInfo{Min: 100, Max: 500, Step: 1, Default: 100}
	if info != want {
		t.Errorf("info = %+v, want %+v", info, want)
	}
}

func TestQueryControlNegativeRange(t *testing.T) {
	minPan := int32(-36000)
	d := testDevice(t, func(fd int, req uintptr, arg unsafe.Pointer) error {
		buf := unsafe.Slice((*byte)(arg), sizeofQueryctrl)
		binary.LittleEndian.PutUint32(buf[40:44], uint32(minPan))
		binary.LittleEndian.PutUint32(buf[44:48], 36000)
		binary.LittleEndian.PutUint32(buf[48:52], 3600)
		return nil
	})
	info, err := d.QueryControl(CtrlPanAbsolute)
	if err != nil {
		t.Fatal(err)
	}
	if info.Min != -36000 || info.Max != 36000 {
		t.Errorf("range = [%d, %d], want [-36000, 36000]", info.Min, info.Max)
	}
}

func TestQueryControlDisabled(t *testing.T) {
	d := testDevice(t, func(fd int, req uintptr, arg unsafe.Pointer) error {
		buf := unsafe.Slice((*byte)(arg), sizeofQueryctrl)
		binary.LittleEndian.PutUint32(buf[56:60], flagDisabled)
		return nil
	})
	if _, err := d.QueryControl(CtrlZoomAbsolute); err == nil {
		t.Error("disabled control should report an error")
	}
}

func TestGetSet(t *testing.T) {
	var setID uint32
	var setValue int32
	d := testDevice(t, func(fd int, req uintptr, arg unsafe.Pointer) error {
		buf := unsafe.Slice((*byte)(arg), sizeofControl)
		switch req {
		case vidiocGCtrl:
			if binary.LittleEndian.Uint32(buf[0:4]) != CtrlTiltAbsolute {
				t.Fatalf("get id %#x", binary.LittleEndian.Uint32(buf[0:4]))
			}
			tilt := int32(-7200)
			binary.LittleEndian.PutUint32(buf[4:8], uint32(tilt))
		case vidiocSCtrl:
			setID = binary.LittleEndian.Uint32(buf[0:4])
			setValue = int32(binary.LittleEndian.Uint32(buf[4:8]))
		default:
			t.Fatalf("unexpected request %#x", req)
		}
		return nil
	})

	v, err := d.Get(CtrlTiltAbsolute)
	if err != nil {
		t.Fatal(err)
	}
	if v != -7200 {
		t.Errorf("Get = %d, want -7200", v)
	}

	if err := d.Set(CtrlZoomAbsolute, 250); err != nil {
		t.Fatal(err)
	}
	if setID != CtrlZoomAbsolute || setValue != 250 {
		t.Errorf("Set wrote id=%#x value=%d", setID, setValue)
	}
}

// testStreamDevice is like testDevice but also fakes mmap/munmap, since
// a streaming Device needs both.
func testStreamDevice(t *testing.T, ioctl ioctlFunc, mmap mmapFunc, munmap munmapFunc) *Device {
	t.Helper()
	d := testDevice(t, ioctl)
	d.mmap = mmap
	d.munmap = munmap
	return d
}

func TestSetFormat(t *testing.T) {
	d := testDevice(t, func(fd int, req uintptr, arg unsafe.Pointer) error {
		if req != vidiocSFmt {
			t.Fatalf("unexpected request %#x", req)
		}
		buf := unsafe.Slice((*byte)(arg), sizeofFormat)
		if got := binary.LittleEndian.Uint32(buf[0:4]); got != bufTypeVideoCapture {
			t.Fatalf("type = %d, want video capture", got)
		}
		if w, h := binary.LittleEndian.Uint32(buf[8:12]), binary.LittleEndian.Uint32(buf[12:16]); w != 640 || h != 480 {
			t.Fatalf("requested %dx%d, want 640x480", w, h)
		}
		// The driver "negotiates" a different pixel format and a
		// tighter bytesperline/sizeimage than what was asked for.
		binary.LittleEndian.PutUint32(buf[16:20], PixFmtYUYV)
		binary.LittleEndian.PutUint32(buf[24:28], 1280)
		binary.LittleEndian.PutUint32(buf[28:32], 614400)
		return nil
	})
	format, err := d.SetFormat(640, 480, PixFmtMJPEG)
	if err != nil {
		t.Fatal(err)
	}
	want := Format{Width: 640, Height: 480, PixelFormat: PixFmtYUYV, BytesPerLine: 1280, SizeImage: 614400}
	if format != want {
		t.Errorf("format = %+v, want %+v", format, want)
	}
}

func TestGetFormat(t *testing.T) {
	d := testDevice(t, func(fd int, req uintptr, arg unsafe.Pointer) error {
		if req != vidiocGFmt {
			t.Fatalf("unexpected request %#x", req)
		}
		buf := unsafe.Slice((*byte)(arg), sizeofFormat)
		if got := binary.LittleEndian.Uint32(buf[0:4]); got != bufTypeVideoCapture {
			t.Fatalf("type = %d, want video capture", got)
		}
		binary.LittleEndian.PutUint32(buf[8:12], 1280)
		binary.LittleEndian.PutUint32(buf[12:16], 720)
		binary.LittleEndian.PutUint32(buf[16:20], PixFmtMJPEG)
		return nil
	})
	format, err := d.GetFormat()
	if err != nil {
		t.Fatal(err)
	}
	want := Format{Width: 1280, Height: 720, PixelFormat: PixFmtMJPEG}
	if format != want {
		t.Errorf("format = %+v, want %+v", format, want)
	}
}

func TestRequestBuffersGrantsFewer(t *testing.T) {
	d := testDevice(t, func(fd int, req uintptr, arg unsafe.Pointer) error {
		buf := unsafe.Slice((*byte)(arg), sizeofRequestBuffers)
		if got := binary.LittleEndian.Uint32(buf[4:8]); got != bufTypeVideoCapture {
			t.Fatalf("type = %d", got)
		}
		if got := binary.LittleEndian.Uint32(buf[8:12]); got != memoryMMAP {
			t.Fatalf("memory = %d, want MMAP", got)
		}
		binary.LittleEndian.PutUint32(buf[0:4], 2) // asked for more, driver grants 2
		return nil
	})
	n, err := d.RequestBuffers(4)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("granted = %d, want 2", n)
	}
}

func TestMapUnmapBuffer(t *testing.T) {
	var mmapFD int
	var mmapOffset int64
	var mmapLength int
	fakeMem := make([]byte, 4096)
	unmapped := false

	d := testStreamDevice(t,
		func(fd int, req uintptr, arg unsafe.Pointer) error {
			if req != vidiocQuerybuf {
				t.Fatalf("unexpected request %#x", req)
			}
			buf := unsafe.Slice((*byte)(arg), sizeofBuffer)
			if idx := binary.LittleEndian.Uint32(buf[0:4]); idx != 2 {
				t.Fatalf("index = %d, want 2", idx)
			}
			binary.LittleEndian.PutUint32(buf[64:68], 8192) // offset
			binary.LittleEndian.PutUint32(buf[72:76], 4096) // length
			return nil
		},
		func(fd int, offset int64, length int) ([]byte, error) {
			mmapFD, mmapOffset, mmapLength = fd, offset, length
			return fakeMem, nil
		},
		func(b []byte) error {
			if &b[0] != &fakeMem[0] {
				t.Errorf("munmap got a different slice")
			}
			unmapped = true
			return nil
		},
	)

	mem, err := d.MapBuffer(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(mem) != 4096 {
		t.Errorf("mapped %d bytes, want 4096", len(mem))
	}
	if mmapOffset != 8192 || mmapLength != 4096 {
		t.Errorf("mmap(offset=%d, length=%d), want (8192, 4096)", mmapOffset, mmapLength)
	}
	if mmapFD != int(d.f.Fd()) {
		t.Errorf("mmap fd = %d, want device fd", mmapFD)
	}

	if err := d.UnmapBuffer(mem); err != nil {
		t.Fatal(err)
	}
	if !unmapped {
		t.Error("UnmapBuffer didn't call munmap")
	}
}

func TestQueueDequeueBuffer(t *testing.T) {
	var queuedIndex uint32 = 999
	d := testDevice(t, func(fd int, req uintptr, arg unsafe.Pointer) error {
		buf := unsafe.Slice((*byte)(arg), sizeofBuffer)
		switch req {
		case vidiocQbuf:
			queuedIndex = binary.LittleEndian.Uint32(buf[0:4])
		case vidiocDqbuf:
			binary.LittleEndian.PutUint32(buf[0:4], 1)     // index
			binary.LittleEndian.PutUint32(buf[8:12], 4096) // bytesused
		default:
			t.Fatalf("unexpected request %#x", req)
		}
		return nil
	})

	if err := d.QueueBuffer(3); err != nil {
		t.Fatal(err)
	}
	if queuedIndex != 3 {
		t.Errorf("queued index = %d, want 3", queuedIndex)
	}

	index, n, err := d.DequeueBuffer()
	if err != nil {
		t.Fatal(err)
	}
	if index != 1 || n != 4096 {
		t.Errorf("DequeueBuffer = (%d, %d), want (1, 4096)", index, n)
	}
}

func TestDequeueBufferPropagatesStreamOffError(t *testing.T) {
	streamOff := errors.New("EPIPE")
	d := testDevice(t, func(fd int, req uintptr, arg unsafe.Pointer) error {
		if req == vidiocDqbuf {
			return streamOff
		}
		return nil
	})
	if _, _, err := d.DequeueBuffer(); !errors.Is(err, streamOff) {
		t.Errorf("DequeueBuffer error = %v, want %v", err, streamOff)
	}
}

func TestStreamOnOff(t *testing.T) {
	var requests []uintptr
	var types []uint32
	d := testDevice(t, func(fd int, req uintptr, arg unsafe.Pointer) error {
		requests = append(requests, req)
		types = append(types, *(*uint32)(arg))
		return nil
	})
	if err := d.StreamOn(); err != nil {
		t.Fatal(err)
	}
	if err := d.StreamOff(); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || requests[0] != vidiocStreamon || requests[1] != vidiocStreamoff {
		t.Errorf("requests = %v", requests)
	}
	for _, ty := range types {
		if ty != bufTypeVideoCapture {
			t.Errorf("type = %d, want video capture", ty)
		}
	}
}

func TestErrorsPropagate(t *testing.T) {
	fail := errors.New("EINVAL")
	d := testDevice(t, func(fd int, req uintptr, arg unsafe.Pointer) error { return fail })
	if _, err := d.QueryControl(CtrlZoomAbsolute); !errors.Is(err, fail) {
		t.Errorf("QueryControl error = %v", err)
	}
	if _, err := d.Get(CtrlZoomAbsolute); !errors.Is(err, fail) {
		t.Errorf("Get error = %v", err)
	}
	if err := d.Set(CtrlZoomAbsolute, 1); !errors.Is(err, fail) {
		t.Errorf("Set error = %v", err)
	}
}
