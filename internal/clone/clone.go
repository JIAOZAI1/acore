// Package clone provides generic copy helpers for reference-backed values.
package clone

// Slice copies a slice and its backing array. Elements are copied by
// assignment, so reference-backed elements still share their nested data.
func Slice[S ~[]E, E any](source S) S {
	if source == nil {
		return nil
	}

	cloned := make(S, len(source))
	copy(cloned, source)
	return cloned
}

// SliceWith copies a slice using cloneElement to copy each element. It can be
// used when elements themselves contain reference-backed values.
func SliceWith[S ~[]E, E any](source S, cloneElement func(E) E) S {
	if source == nil {
		return nil
	}

	cloned := make(S, len(source))
	for index, element := range source {
		cloned[index] = cloneElement(element)
	}
	return cloned
}
