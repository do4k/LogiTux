// Package config persists the last-known state of each device LogiTux has
// controlled. Litra lights can't report their current brightness, color
// temperature, or power state back over USB, so LogiTux remembers the last
// values it sent and uses them to seed the GUI and to compute relative
// adjustments (e.g. "brightness up 10").
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// DeviceState is the last-known state of a single device, keyed by serial
// number in Store.
type DeviceState struct {
	Power       bool `json:"power"`
	Brightness  int  `json:"brightness"`
	Temperature int  `json:"temperature"`

	DPI        int  `json:"dpi,omitempty"`
	ReportRate int  `json:"report_rate,omitempty"`
	Red        byte `json:"red,omitempty"`
	Green      byte `json:"green,omitempty"`
	Blue       byte `json:"blue,omitempty"`

	// ButtonRemaps maps a device-defined control ID to the uinput target
	// code (see internal/uinput.Targets) it's remapped to. Absent entries
	// mean "default (unremapped)".
	ButtonRemaps map[uint16]uint16 `json:"button_remaps,omitempty"`

	Sidetone        int   `json:"sidetone,omitempty"`
	EqualizerLevels []int `json:"equalizer_levels,omitempty"` // one dB value per band, in device.EqualizerControl's band order
}

// Store is a JSON-backed, serial-number-keyed table of DeviceState,
// persisted to disk on every Set.
type Store struct {
	path string

	mu     sync.Mutex
	states map[string]DeviceState
}

// DefaultPath returns the standard location for LogiTux's state file,
// honoring XDG_CONFIG_HOME (via os.UserConfigDir) with the usual
// ~/.config fallback.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: resolve config dir: %w", err)
	}
	return filepath.Join(dir, "logitux", "state.json"), nil
}

// Open loads the state file at path, if it exists, and returns a Store
// backed by it. A missing file is not an error; Open starts empty and
// creates the file on the first Set.
func Open(path string) (*Store, error) {
	s := &Store{path: path, states: make(map[string]DeviceState)}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(data, &s.states); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return s, nil
}

// Get returns the last-known state for serial, if any.
func (s *Store) Get(serial string) (DeviceState, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, ok := s.states[serial]
	return state, ok
}

// Set records state for serial and persists the whole store to disk.
func (s *Store) Set(serial string, state DeviceState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.states[serial] = state
	return s.saveLocked()
}

// saveLocked writes the store to disk atomically (write to a temp file in
// the same directory, then rename over the target) so a crash mid-write
// can't leave a truncated state file. Callers must hold s.mu.
func (s *Store) saveLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("config: create %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(s.states, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encode state: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".state-*.json.tmp")
	if err != nil {
		return fmt.Errorf("config: create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("config: write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return fmt.Errorf("config: rename into place: %w", err)
	}
	return nil
}
