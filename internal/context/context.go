package context

import (
	"fmt"
	"os"
	"strings"
)

// DirectoryContext contains information about the current directory
type DirectoryContext struct {
	CurrentDir string   `json:"current_dir"`
	Files      []string `json:"files"`
}

// GetDirectoryContext gets information about the current directory
func GetDirectoryContext() (*DirectoryContext, error) {
	// Getting the current directory
	currentDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("error getting the current directory: %w", err)
	}
	
	// Reading directory contents
	entries, err := os.ReadDir(currentDir)
	if err != nil {
		return nil, fmt.Errorf("error reading directory contents: %w", err)
	}
	
	// Creating a list of files
	var files []string
	for _, entry := range entries {
		// Skip hidden files
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		
		// Add directory indicator
		fileName := entry.Name()
		if entry.IsDir() {
			fileName += "/"
		}
		
		files = append(files, fileName)
	}
	
	return &DirectoryContext{
		CurrentDir: currentDir,
		Files:      files,
	}, nil
} 