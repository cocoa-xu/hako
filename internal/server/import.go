package server

import "fmt"

// ImportLegacy imports an exported prototype registry into an empty database.
func (s *Server) ImportLegacy(directory string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.importLegacy(directory)
	if reload := s.loadRegistry(); reload != nil {
		return fmt.Errorf("reload imported registry: %w", reload)
	}
	if reload := s.loadMachines(); reload != nil {
		return fmt.Errorf("reload imported snapshots: %w", reload)
	}
	return err
}
