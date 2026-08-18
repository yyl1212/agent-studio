package safepath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateWriteTarget rejects lexical root escapes and symbolic links below root.
func ValidateWriteTarget(root, target string) error {
	rootPath, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve root: %w", err)
	}
	targetPath, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve target: %w", err)
	}
	relative, err := filepath.Rel(rootPath, targetPath)
	if err != nil {
		return fmt.Errorf("relate target to root: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("write target %s escapes project root %s", targetPath, rootPath)
	}
	if relative == "." {
		return nil
	}
	current := rootPath
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect write path %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("write path %s contains a symbolic link", current)
		}
	}
	return nil
}
