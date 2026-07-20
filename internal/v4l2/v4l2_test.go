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
