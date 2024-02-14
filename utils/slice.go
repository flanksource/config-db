package utils

import "sync"

type SyncBuffer[T any] struct {
	size  int
	m     *sync.RWMutex
	slice []T
}

func NewSyncBuffer[T any](size int) *SyncBuffer[T] {
	return &SyncBuffer[T]{
		size:  size,
		m:     &sync.RWMutex{},
		slice: make([]T, 0, size),
	}
}

func (s *SyncBuffer[T]) Append(t T) {
	s.m.Lock()
	s.slice = append(s.slice, t)
	s.m.Unlock()
}

func (s *SyncBuffer[T]) Drain() []T {
	s.m.RLock()
	copy := s.slice[:]
	s.slice = make([]T, 0, s.size)
	s.m.RUnlock()
	return copy
}
