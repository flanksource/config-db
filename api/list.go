package api

import "sync"

type Aliased interface {
	GetAliases() []string
	SetAliases([]string)
}

type List[P interface {
	*T
	Aliased
}, T any] struct {
	mu    sync.Mutex
	items []T
}

func NewList[P interface {
	*T
	Aliased
}, T any]() *List[P, T] {
	return &List[P, T]{}
}

func (l *List[P, T]) Upsert(item T) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p := P(&item)
	for i := range l.items {
		existing := P(&l.items[i])
		if hasAliasOverlap(existing.GetAliases(), p.GetAliases()) {
			existing.SetAliases(mergeAliases(existing.GetAliases(), p.GetAliases()))
			return
		}
	}
	l.items = append(l.items, item)
}

func (l *List[P, T]) Items() []T {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]T(nil), l.items...)
}

func hasAliasOverlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

func mergeAliases(dst, src []string) []string {
	seen := make(map[string]struct{}, len(dst))
	for _, a := range dst {
		seen[a] = struct{}{}
	}
	for _, a := range src {
		if _, ok := seen[a]; !ok {
			dst = append(dst, a)
		}
	}
	return dst
}
