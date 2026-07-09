package store

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/sviniabanditka/pinctl/internal/model"
)

// ListPipelines returns all known pipelines, ordered by ID.
func (s *Store) ListPipelines() []model.Pipeline {
	s.mu.Lock()
	defer s.mu.Unlock()

	list := make([]model.Pipeline, 0, len(s.pipelines))
	for _, p := range s.pipelines {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list
}

// GetPipeline returns the pipeline with the given ID, or ErrNotFound.
func (s *Store) GetPipeline(id string) (model.Pipeline, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.pipelines[id]
	if !ok {
		return model.Pipeline{}, fmt.Errorf("store: pipeline %s: %w", id, ErrNotFound)
	}
	return p, nil
}

// CreatePipeline assigns a new ID, schemaVersion and timestamps to p,
// validates it, and writes pipelines/<id>.json. Graph-level validation
// (cycles, port references, etc.) is internal/engine's job, not this
// package's — see the package comment.
func (s *Store) CreatePipeline(p model.Pipeline) (model.Pipeline, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p.Name == "" {
		return model.Pipeline{}, ErrNameEmpty
	}

	now := time.Now().UTC()
	p.ID = model.NewID("pl")
	p.SchemaVersion = currentPipelineSchemaVersion
	p.CreatedAt = now
	p.UpdatedAt = now
	if p.Nodes == nil {
		p.Nodes = []model.Node{}
	}
	if p.Edges == nil {
		p.Edges = []model.Edge{}
	}

	s.pipelines[p.ID] = p
	if err := s.savePipelineLocked(p); err != nil {
		delete(s.pipelines, p.ID)
		return model.Pipeline{}, err
	}
	return p, nil
}

// UpdatePipeline replaces the mutable fields (Name, Enabled, TickMs,
// Nodes, Edges) of the pipeline with the given ID, preserving its ID,
// SchemaVersion and CreatedAt, bumping UpdatedAt, and rewriting its file.
func (s *Store) UpdatePipeline(id string, p model.Pipeline) (model.Pipeline, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.pipelines[id]
	if !ok {
		return model.Pipeline{}, fmt.Errorf("store: pipeline %s: %w", id, ErrNotFound)
	}
	if p.Name == "" {
		return model.Pipeline{}, ErrNameEmpty
	}

	updated := p
	updated.ID = existing.ID
	updated.SchemaVersion = currentPipelineSchemaVersion
	updated.CreatedAt = existing.CreatedAt
	updated.UpdatedAt = time.Now().UTC()
	if updated.Nodes == nil {
		updated.Nodes = []model.Node{}
	}
	if updated.Edges == nil {
		updated.Edges = []model.Edge{}
	}

	prev := s.pipelines[id]
	s.pipelines[id] = updated
	if err := s.savePipelineLocked(updated); err != nil {
		s.pipelines[id] = prev
		return model.Pipeline{}, err
	}
	return updated, nil
}

// SetEnabled flips a pipeline's enabled flag, bumps UpdatedAt, and
// rewrites its file. It does not re-validate the graph — that already
// happened at Create/Update time (docs/09-data-model.md's "enabled is
// persisted, re-enabled automatically on restart").
func (s *Store) SetEnabled(id string, enabled bool) (model.Pipeline, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.pipelines[id]
	if !ok {
		return model.Pipeline{}, fmt.Errorf("store: pipeline %s: %w", id, ErrNotFound)
	}

	prev := p
	p.Enabled = enabled
	p.UpdatedAt = time.Now().UTC()

	s.pipelines[id] = p
	if err := s.savePipelineLocked(p); err != nil {
		s.pipelines[id] = prev
		return model.Pipeline{}, err
	}
	return p, nil
}

// DeletePipeline removes the pipeline's file and drops it from memory.
func (s *Store) DeletePipeline(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.pipelines[id]
	if !ok {
		return fmt.Errorf("store: pipeline %s: %w", id, ErrNotFound)
	}

	delete(s.pipelines, id)
	if err := os.Remove(s.pipelinePath(id)); err != nil && !os.IsNotExist(err) {
		s.pipelines[id] = p
		return fmt.Errorf("store: deleting pipeline %s: %w", id, err)
	}
	return nil
}
