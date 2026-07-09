// Package store implements JSON persistence for pinctl's device and
// pipeline objects, per docs/09-data-model.md: a single devices.json
// collection file plus one file per pipeline under pipelines/, both
// written with the write-temp-then-rename atomic strategy described
// there.
//
// v0.2 is single-board (docs/12-roadmap.md), so boards.json is
// deliberately not implemented here — multi-board persistence lands in
// v0.4. Device.BoardID is stored verbatim but never validated against a
// board collection by this package.
//
// Graph-level pipeline validation (cycles, dangling ports, node/edge
// referential integrity — docs/09-data-model.md "Validation rules") is
// internal/engine's job, not store's: this package only enforces JSON
// well-formedness, the (boardId, pin) uniqueness rule (FR-DEVICE-4), and
// non-empty names before writing anything to disk.
//
// Referential integrity between pipelines and devices — a device cannot be
// deleted while any pipeline still references it (docs/09-data-model.md
// "Validation rules") — is enforced by DeleteDevice itself, returning
// ErrDeviceInUse, in addition to being checked at pipeline enable/validate
// time.
package store

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/sviniabanditka/pinctl/internal/model"
)

const (
	currentDeviceSchemaVersion   = 1
	currentPipelineSchemaVersion = 1
)

const filePerm = 0o644

// Store is the in-memory, mutex-guarded, write-through cache of everything
// under a pinctl data directory. All mutating methods persist to disk
// before returning; a successful call means the change is durable.
type Store struct {
	dir          string
	devicesPath  string
	pipelinesDir string
	tmpDir       string

	mu        sync.Mutex
	devices   map[string]model.Device
	pipelines map[string]model.Pipeline
}

// devicesFile is the on-disk shape of devices.json (docs/09-data-model.md
// "Device object").
type devicesFile struct {
	SchemaVersion int            `json:"schemaVersion"`
	Devices       []model.Device `json:"devices"`
}

// Open loads dir's devices.json and pipelines/*.json into memory, creating
// the directory layout (dir, dir/pipelines, dir/.tmp) on first use if it
// doesn't exist yet. Missing files are treated as empty collections, not
// an error. Malformed or unknown-schema-version documents are skipped
// with a logged warning rather than failing Open, per
// docs/09-data-model.md's versioning policy ("skipped, not fatal to
// startup").
func Open(dir string) (*Store, error) {
	s := &Store{
		dir:          dir,
		devicesPath:  filepath.Join(dir, "devices.json"),
		pipelinesDir: filepath.Join(dir, "pipelines"),
		tmpDir:       filepath.Join(dir, ".tmp"),
		devices:      make(map[string]model.Device),
		pipelines:    make(map[string]model.Pipeline),
	}

	if err := os.MkdirAll(s.pipelinesDir, 0o755); err != nil {
		return nil, fmt.Errorf("store: creating pipelines dir: %w", err)
	}
	if err := os.MkdirAll(s.tmpDir, 0o755); err != nil {
		return nil, fmt.Errorf("store: creating tmp dir: %w", err)
	}

	if err := s.loadDevices(); err != nil {
		return nil, err
	}
	if err := s.loadPipelines(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *Store) loadDevices() error {
	data, err := os.ReadFile(s.devicesPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: reading devices.json: %w", err)
	}

	var f devicesFile
	if err := json.Unmarshal(data, &f); err != nil {
		log.Printf("store: devices.json is corrupted, skipping (starting with an empty device list): %v", err)
		return nil
	}
	if f.SchemaVersion > currentDeviceSchemaVersion {
		log.Printf("store: devices.json has schemaVersion %d, newer than this build understands (%d); upgrade pinctl. Skipping.", f.SchemaVersion, currentDeviceSchemaVersion)
		return nil
	}

	for _, d := range f.Devices {
		s.devices[d.ID] = d
	}
	return nil
}

func (s *Store) loadPipelines() error {
	entries, err := os.ReadDir(s.pipelinesDir)
	if err != nil {
		return fmt.Errorf("store: reading pipelines dir: %w", err)
	}

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(s.pipelinesDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			log.Printf("store: reading %s failed, skipping: %v", path, err)
			continue
		}

		var p model.Pipeline
		if err := json.Unmarshal(data, &p); err != nil {
			log.Printf("store: %s is corrupted, skipping: %v", path, err)
			continue
		}
		if p.SchemaVersion > currentPipelineSchemaVersion {
			log.Printf("store: %s has schemaVersion %d, newer than this build understands (%d); upgrade pinctl. Skipping.", path, p.SchemaVersion, currentPipelineSchemaVersion)
			continue
		}

		s.pipelines[p.ID] = p
	}
	return nil
}

// saveDevicesLocked rewrites the whole devices.json collection. Caller
// must hold s.mu.
func (s *Store) saveDevicesLocked() error {
	list := make([]model.Device, 0, len(s.devices))
	for _, d := range s.devices {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })

	data, err := json.MarshalIndent(devicesFile{
		SchemaVersion: currentDeviceSchemaVersion,
		Devices:       list,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("store: marshaling devices.json: %w", err)
	}
	return writeFileAtomic(s.dir, s.tmpDir, s.devicesPath, data, filePerm)
}

// savePipelineLocked writes a single pipeline's own file. Caller must hold
// s.mu.
func (s *Store) savePipelineLocked(p model.Pipeline) error {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("store: marshaling pipeline %s: %w", p.ID, err)
	}
	return writeFileAtomic(s.pipelinesDir, s.tmpDir, s.pipelinePath(p.ID), data, filePerm)
}

func (s *Store) pipelinePath(id string) string {
	return filepath.Join(s.pipelinesDir, id+".json")
}
