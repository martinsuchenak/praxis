package bot

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// ReadLastNLines reads the last n lines from the file at path.
// Returns an error if the file is empty or cannot be opened.
func ReadLastNLines(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > n*2 {
			lines = lines[len(lines)-n:]
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan: %w", err)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	if len(lines) == 0 {
		return "", fmt.Errorf("empty")
	}
	return strings.Join(lines, "\n") + "\n", nil
}
