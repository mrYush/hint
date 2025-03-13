package context

import (
	"fmt"
	"os"
	"strings"
)

// DirectoryContext содержит информацию о текущей директории
type DirectoryContext struct {
	CurrentDir string   `json:"current_dir"`
	Files      []string `json:"files"`
}

// GetDirectoryContext получает информацию о текущей директории
func GetDirectoryContext() (*DirectoryContext, error) {
	// Получение текущей директории
	currentDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("ошибка получения текущей директории: %w", err)
	}
	
	// Чтение содержимого директории
	entries, err := os.ReadDir(currentDir)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения содержимого директории: %w", err)
	}
	
	// Формирование списка файлов
	var files []string
	for _, entry := range entries {
		// Пропускаем скрытые файлы
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		
		// Добавляем индикатор директории
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