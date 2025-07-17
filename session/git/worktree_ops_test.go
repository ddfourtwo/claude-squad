package git

import (
	"claude-squad/config"
	"claude-squad/log"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain runs before all tests to set up the test environment
func TestMain(m *testing.M) {
	// Initialize the logger before any tests run
	log.Initialize(false)
	defer log.Close()

	exitCode := m.Run()
	os.Exit(exitCode)
}

func TestCopyConfiguredFiles(t *testing.T) {
	t.Run("copies configured files from repo to worktree", func(t *testing.T) {
		// Create temporary directories for repo and worktree
		tempDir := t.TempDir()
		repoPath := filepath.Join(tempDir, "repo")
		worktreePath := filepath.Join(tempDir, "worktree")
		
		// Create repo directory structure
		require.NoError(t, os.MkdirAll(filepath.Join(repoPath, "config"), 0755))
		require.NoError(t, os.MkdirAll(worktreePath, 0755))
		
		// Create test files in repo
		envContent := "API_KEY=secret123"
		envLocalContent := "LOCAL_KEY=local456"
		configContent := `{"secret": "value"}`
		
		require.NoError(t, os.WriteFile(filepath.Join(repoPath, ".env"), []byte(envContent), 0600))
		require.NoError(t, os.WriteFile(filepath.Join(repoPath, ".env.local"), []byte(envLocalContent), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(repoPath, "config", "secrets.json"), []byte(configContent), 0600))
		
		// Override HOME to use a temporary config
		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tempDir)
		defer os.Setenv("HOME", originalHome)
		
		// Create config directory and file
		configDir := filepath.Join(tempDir, ".claude-squad")
		require.NoError(t, os.MkdirAll(configDir, 0755))
		
		// Save test config
		testConfig := &config.Config{
			DefaultProgram:     "claude",
			AutoYes:            false,
			DaemonPollInterval: 1000,
			BranchPrefix:       "test/",
			CopyOnCreate:       []string{".env", ".env.local", "config/secrets.json"},
		}
		require.NoError(t, config.SaveConfig(testConfig))
		
		// Create GitWorktree instance
		g := &GitWorktree{
			repoPath:     repoPath,
			worktreePath: worktreePath,
		}
		
		// Execute the copy
		err := g.copyConfiguredFiles()
		assert.NoError(t, err)
		
		// Verify files were copied
		assert.FileExists(t, filepath.Join(worktreePath, ".env"))
		assert.FileExists(t, filepath.Join(worktreePath, ".env.local"))
		assert.FileExists(t, filepath.Join(worktreePath, "config", "secrets.json"))
		
		// Verify content
		copiedEnv, err := os.ReadFile(filepath.Join(worktreePath, ".env"))
		assert.NoError(t, err)
		assert.Equal(t, envContent, string(copiedEnv))
		
		copiedConfig, err := os.ReadFile(filepath.Join(worktreePath, "config", "secrets.json"))
		assert.NoError(t, err)
		assert.Equal(t, configContent, string(copiedConfig))
		
		// Verify permissions were preserved
		envInfo, err := os.Stat(filepath.Join(worktreePath, ".env"))
		assert.NoError(t, err)
		assert.Equal(t, os.FileMode(0600), envInfo.Mode().Perm())
	})
	
	t.Run("skips missing files gracefully", func(t *testing.T) {
		// Create temporary directories
		tempDir := t.TempDir()
		repoPath := filepath.Join(tempDir, "repo")
		worktreePath := filepath.Join(tempDir, "worktree")
		
		require.NoError(t, os.MkdirAll(repoPath, 0755))
		require.NoError(t, os.MkdirAll(worktreePath, 0755))
		
		// Create only one of the configured files
		require.NoError(t, os.WriteFile(filepath.Join(repoPath, ".env"), []byte("KEY=value"), 0644))
		
		// Override HOME
		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tempDir)
		defer os.Setenv("HOME", originalHome)
		
		// Create config
		configDir := filepath.Join(tempDir, ".claude-squad")
		require.NoError(t, os.MkdirAll(configDir, 0755))
		
		testConfig := &config.Config{
			DefaultProgram:     "claude",
			AutoYes:            false,
			DaemonPollInterval: 1000,
			BranchPrefix:       "test/",
			CopyOnCreate:       []string{".env", ".env.local", "missing.txt"},
		}
		require.NoError(t, config.SaveConfig(testConfig))
		
		// Create GitWorktree
		g := &GitWorktree{
			repoPath:     repoPath,
			worktreePath: worktreePath,
		}
		
		// Execute - should not error on missing files
		err := g.copyConfiguredFiles()
		assert.NoError(t, err)
		
		// Verify only existing file was copied
		assert.FileExists(t, filepath.Join(worktreePath, ".env"))
		assert.NoFileExists(t, filepath.Join(worktreePath, ".env.local"))
		assert.NoFileExists(t, filepath.Join(worktreePath, "missing.txt"))
	})
	
	t.Run("handles empty copy_on_create list", func(t *testing.T) {
		// Create temporary directories
		tempDir := t.TempDir()
		repoPath := filepath.Join(tempDir, "repo")
		worktreePath := filepath.Join(tempDir, "worktree")
		
		require.NoError(t, os.MkdirAll(repoPath, 0755))
		require.NoError(t, os.MkdirAll(worktreePath, 0755))
		
		// Override HOME
		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tempDir)
		defer os.Setenv("HOME", originalHome)
		
		// Create config with empty CopyOnCreate
		configDir := filepath.Join(tempDir, ".claude-squad")
		require.NoError(t, os.MkdirAll(configDir, 0755))
		
		testConfig := &config.Config{
			DefaultProgram:     "claude",
			AutoYes:            false,
			DaemonPollInterval: 1000,
			BranchPrefix:       "test/",
			CopyOnCreate:       []string{}, // Empty list
		}
		require.NoError(t, config.SaveConfig(testConfig))
		
		// Create GitWorktree
		g := &GitWorktree{
			repoPath:     repoPath,
			worktreePath: worktreePath,
		}
		
		// Execute - should handle empty list gracefully
		err := g.copyConfiguredFiles()
		assert.NoError(t, err)
	})
}