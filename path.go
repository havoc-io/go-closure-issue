package rsync

import (
	"path/filepath"
	"strings"
)

type Path []string

func (p Path) Appended(component string) Path {
	// Compute the length of the existing path.
	existingLength := len(p)

	// Allocate the new path.
	result := make(Path, existingLength+1)

	// Copy in the existing path components.
	copy(result, p)

	// Tack on the new path component.
	result[existingLength] = component

	// Done.
	return result
}

func (p Path) String() string {
	return strings.Join([]string(p), "/")
}

func (p Path) AppendedToRoot(root string) string {
	// Join the path itself.
	path := filepath.Join(p...)

	// Append it to the root.
	return filepath.Join(root, path)
}
