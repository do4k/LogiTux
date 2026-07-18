package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestOpenMissingFileStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logitux", "state.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, ok := s.Get("anything"); ok {
		t.Error("expected no state for an unseeded store")
	}
}

func TestSetThenReopenRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logitux", "state.json")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	want := DeviceState{
		Power:        true,
		Brightness:   42,
		Temperature:  4000,
		ButtonRemaps: map[uint16]uint16{0x53: 30},
	}
	if err := s.Set("SN1", want); err != nil {
		t.Fatalf("Set: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok := reopened.Get("SN1")
	if !ok {
		t.Fatal("expected state for SN1 after reopen")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestSetOverwritesExistingSerial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logitux", "state.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := s.Set("SN1", DeviceState{Brightness: 10}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Set("SN1", DeviceState{Brightness: 90}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, _ := s.Get("SN1")
	if got.Brightness != 90 {
		t.Errorf("expected latest Set to win, got brightness %d", got.Brightness)
	}
}

func TestMultipleSerialsAreIndependent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logitux", "state.json")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := s.Set("SN1", DeviceState{Brightness: 10}); err != nil {
		t.Fatalf("Set SN1: %v", err)
	}
	if err := s.Set("SN2", DeviceState{Brightness: 20}); err != nil {
		t.Fatalf("Set SN2: %v", err)
	}

	got1, _ := s.Get("SN1")
	got2, _ := s.Get("SN2")
	if got1.Brightness != 10 || got2.Brightness != 20 {
		t.Errorf("expected independent state, got SN1=%+v SN2=%+v", got1, got2)
	}
}
