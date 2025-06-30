package claude

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ConversationInfo holds basic info about a Claude conversation
type ConversationInfo struct {
	SessionID string
	Title     string
	Path      string
}

// GetClaudeProjectPath returns the Claude project directory for a given repo path
func GetClaudeProjectPath(repoPath string) string {
	// Convert absolute path to Claude's format
	// /Users/daniel/Documents/GitHub/claude-squad → -Users-daniel-Documents-GitHub-claude-squad
	cleanPath := strings.ReplaceAll(repoPath, "/", "-")
	if strings.HasPrefix(cleanPath, "-") {
		cleanPath = cleanPath[1:]
	}
	cleanPath = "-" + cleanPath
	
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".claude", "projects", cleanPath)
}

// ListConversations returns all conversations for a given project path
func ListConversations(projectPath string) ([]ConversationInfo, error) {
	claudePath := GetClaudeProjectPath(projectPath)
	
	entries, err := os.ReadDir(claudePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []ConversationInfo{}, nil
		}
		return nil, fmt.Errorf("failed to read Claude project directory: %w", err)
	}
	
	var conversations []ConversationInfo
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			sessionID := strings.TrimSuffix(entry.Name(), ".jsonl")
			
			// Try to get the title from the conversation file
			title := getConversationTitle(filepath.Join(claudePath, entry.Name()))
			
			conversations = append(conversations, ConversationInfo{
				SessionID: sessionID,
				Title:     title,
				Path:      filepath.Join(claudePath, entry.Name()),
			})
		}
	}
	
	return conversations, nil
}

// getConversationTitle reads the first few lines of a conversation file to find the title
func getConversationTitle(filePath string) string {
	file, err := os.Open(filePath)
	if err != nil {
		return "Untitled"
	}
	defer file.Close()
	
	// Read first few KB to find the summary
	buf := make([]byte, 8192)
	n, _ := file.Read(buf)
	
	lines := strings.Split(string(buf[:n]), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		
		var msg map[string]interface{}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		
		if msg["type"] == "summary" {
			if summaryData, ok := msg["summary"].(map[string]interface{}); ok {
				if title, ok := summaryData["title"].(string); ok {
					return title
				}
			}
		}
	}
	
	return "Untitled"
}

// CopyConversation copies a Claude conversation from one project to another
func CopyConversation(sourceProjectPath, targetProjectPath, sessionID string) error {
	sourcePath := filepath.Join(GetClaudeProjectPath(sourceProjectPath), sessionID+".jsonl")
	targetDir := GetClaudeProjectPath(targetProjectPath)
	targetPath := filepath.Join(targetDir, sessionID+".jsonl")
	
	// Create target directory if it doesn't exist
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}
	
	// Copy the file
	sourceFile, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("failed to open source file: %w", err)
	}
	defer sourceFile.Close()
	
	targetFile, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("failed to create target file: %w", err)
	}
	defer targetFile.Close()
	
	if _, err := io.Copy(targetFile, sourceFile); err != nil {
		return fmt.Errorf("failed to copy conversation: %w", err)
	}
	
	return nil
}