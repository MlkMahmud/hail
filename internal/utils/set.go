package utils

type Set struct {
	entries map[string]struct{}
}

func NewSet() *Set {
	return &Set{
		entries: make(map[string]struct{}),
	}
}

func (s *Set) Add(entry string) {
	s.entries[entry] = struct{}{}
}

func (s *Set) Entries() map[string]struct{} {
	return s.entries
}

func (s *Set) Remove(entry string) {
	delete(s.entries, entry)
}

func (s *Set) Contains(entry string) bool {
	_, exists := s.entries[entry]
	return exists
}

func (s *Set) Size() int {
	return len(s.entries)
}

func (s *Set) Clear() {
	s.entries = make(map[string]struct{})
}
