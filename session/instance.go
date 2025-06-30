package session

import (
	"claude-squad/log"
	"claude-squad/session/git"
	"claude-squad/session/tmux"
	"io"
	"path/filepath"

	"fmt"
	"os"
	"strings"
	"time"

	"github.com/atotto/clipboard"
)

type Status int

const (
	// Running is the status when the instance is running and claude is working.
	Running Status = iota
	// Ready is if the claude instance is ready to be interacted with (waiting for user input).
	Ready
	// Loading is if the instance is loading (if we are starting it up or something).
	Loading
	// Paused is if the instance is paused (worktree removed but branch preserved).
	Paused
)

// Instance is a running instance of claude code.
type Instance struct {
	// Title is the title of the instance.
	Title string
	// Path is the path to the workspace.
	Path string
	// Branch is the branch of the instance.
	Branch string
	// Status is the status of the instance.
	Status Status
	// Program is the program to run in the instance.
	Program string
	// Height is the height of the instance.
	Height int
	// Width is the width of the instance.
	Width int
	// CreatedAt is the time the instance was created.
	CreatedAt time.Time
	// UpdatedAt is the time the instance was last updated.
	UpdatedAt time.Time
	// AutoYes is true if the instance should automatically press enter when prompted.
	AutoYes bool
	// Prompt is the initial prompt to pass to the instance on startup
	Prompt string
	// ClaudeResume indicates if this instance should start with claude --resume
	ClaudeResume bool

	// DiffStats stores the current git diff statistics
	diffStats *git.DiffStats

	// The below fields are initialized upon calling Start().

	started bool
	// tmuxSession is the tmux session for the instance.
	tmuxSession *tmux.TmuxSession
	// gitWorktree is the git worktree for the instance.
	gitWorktree *git.GitWorktree
}

// ToInstanceData converts an Instance to its serializable form
func (i *Instance) ToInstanceData() InstanceData {
	data := InstanceData{
		Title:     i.Title,
		Path:      i.Path,
		Branch:    i.Branch,
		Status:    i.Status,
		Height:    i.Height,
		Width:     i.Width,
		CreatedAt: i.CreatedAt,
		UpdatedAt: time.Now(),
		Program:   i.Program,
		AutoYes:   i.AutoYes,
	}

	// Only include worktree data if gitWorktree is initialized
	if i.gitWorktree != nil {
		data.Worktree = GitWorktreeData{
			RepoPath:      i.gitWorktree.GetRepoPath(),
			WorktreePath:  i.gitWorktree.GetWorktreePath(),
			SessionName:   i.Title,
			BranchName:    i.gitWorktree.GetBranchName(),
			BaseCommitSHA: i.gitWorktree.GetBaseCommitSHA(),
		}
	}

	// Only include diff stats if they exist
	if i.diffStats != nil {
		data.DiffStats = DiffStatsData{
			Added:   i.diffStats.Added,
			Removed: i.diffStats.Removed,
			Content: i.diffStats.Content,
		}
	}

	return data
}

// FromInstanceData creates a new Instance from serialized data
func FromInstanceData(data InstanceData) (*Instance, error) {
	instance := &Instance{
		Title:     data.Title,
		Path:      data.Path,
		Branch:    data.Branch,
		Status:    data.Status,
		Height:    data.Height,
		Width:     data.Width,
		CreatedAt: data.CreatedAt,
		UpdatedAt: data.UpdatedAt,
		Program:   data.Program,
		gitWorktree: git.NewGitWorktreeFromStorage(
			data.Worktree.RepoPath,
			data.Worktree.WorktreePath,
			data.Worktree.SessionName,
			data.Worktree.BranchName,
			data.Worktree.BaseCommitSHA,
		),
		diffStats: &git.DiffStats{
			Added:   data.DiffStats.Added,
			Removed: data.DiffStats.Removed,
			Content: data.DiffStats.Content,
		},
	}

	if instance.Paused() {
		instance.started = true
		instance.tmuxSession = tmux.NewTmuxSession(instance.Title, instance.Program)
	} else {
		if err := instance.Start(false); err != nil {
			return nil, err
		}
	}

	return instance, nil
}

// Options for creating a new instance
type InstanceOptions struct {
	// Title is the title of the instance.
	Title string
	// Path is the path to the workspace.
	Path string
	// Program is the program to run in the instance (e.g. "claude", "aider --model ollama_chat/gemma3:1b")
	Program string
	// If AutoYes is true, then
	AutoYes bool
}

func NewInstance(opts InstanceOptions) (*Instance, error) {
	t := time.Now()

	// Convert path to absolute
	absPath, err := filepath.Abs(opts.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}

	return &Instance{
		Title:     opts.Title,
		Status:    Ready,
		Path:      absPath,
		Program:   opts.Program,
		Height:    0,
		Width:     0,
		CreatedAt: t,
		UpdatedAt: t,
		AutoYes:   false,
	}, nil
}

func (i *Instance) RepoName() (string, error) {
	if !i.started {
		return "", fmt.Errorf("cannot get repo name for instance that has not been started")
	}
	return i.gitWorktree.GetRepoName(), nil
}

func (i *Instance) SetStatus(status Status) {
	i.Status = status
}

// firstTimeSetup is true if this is a new instance. Otherwise, it's one loaded from storage.
func (i *Instance) Start(firstTimeSetup bool) error {
	if i.Title == "" {
		return fmt.Errorf("instance title cannot be empty")
	}

	// Don't modify the program for ClaudeResume - we'll handle it differently
	tmuxSession := tmux.NewTmuxSession(i.Title, i.Program)
	i.tmuxSession = tmuxSession

	if firstTimeSetup {
		gitWorktree, branchName, err := git.NewGitWorktree(i.Path, i.Title)
		if err != nil {
			return fmt.Errorf("failed to create git worktree: %w", err)
		}
		i.gitWorktree = gitWorktree
		i.Branch = branchName
	}

	// Setup error handler to cleanup resources on any error
	var setupErr error
	defer func() {
		if setupErr != nil {
			if cleanupErr := i.Kill(); cleanupErr != nil {
				setupErr = fmt.Errorf("%v (cleanup error: %v)", setupErr, cleanupErr)
			}
		} else {
			i.started = true
		}
	}()

	if !firstTimeSetup {
		// Reuse existing session
		if err := tmuxSession.Restore(); err != nil {
			setupErr = fmt.Errorf("failed to restore existing session: %w", err)
			return setupErr
		}
	} else {
		// Setup git worktree first
		if err := i.gitWorktree.Setup(); err != nil {
			setupErr = fmt.Errorf("failed to setup git worktree: %w", err)
			return setupErr
		}

		// Create new session
		if err := i.tmuxSession.Start(i.gitWorktree.GetWorktreePath()); err != nil {
			// Cleanup git worktree if tmux session creation fails
			if cleanupErr := i.gitWorktree.Cleanup(); cleanupErr != nil {
				err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
			}
			setupErr = fmt.Errorf("failed to start new session: %w", err)
			return setupErr
		}
	}

	// If ClaudeResume is set, prepare conversations before starting
	if i.ClaudeResume && strings.Contains(i.Program, "claude") && firstTimeSetup {
		// Copy Claude conversations from the original project to the worktree
		// Do this BEFORE Claude starts so they're available immediately
		if err := prepareClaudeConversations(i.Path, i.gitWorktree.GetWorktreePath()); err != nil {
			log.ErrorLog.Printf("Failed to prepare Claude conversations: %v", err)
		} else {
			log.InfoLog.Printf("Successfully prepared Claude conversations for worktree")
		}
	}
	
	i.SetStatus(Running)
	

	return nil
}

// Kill terminates the instance and cleans up all resources
func (i *Instance) Kill() error {
	if !i.started {
		// If instance was never started, just return success
		return nil
	}

	var errs []error

	// Always try to cleanup both resources, even if one fails
	// Clean up tmux session first since it's using the git worktree
	if i.tmuxSession != nil {
		if err := i.tmuxSession.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close tmux session: %w", err))
		}
	}

	// Then clean up git worktree
	if i.gitWorktree != nil {
		if err := i.gitWorktree.Cleanup(); err != nil {
			errs = append(errs, fmt.Errorf("failed to cleanup git worktree: %w", err))
		}
	}

	return i.combineErrors(errs)
}

// combineErrors combines multiple errors into a single error
func (i *Instance) combineErrors(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}

	errMsg := "multiple cleanup errors occurred:"
	for _, err := range errs {
		errMsg += "\n  - " + err.Error()
	}
	return fmt.Errorf("%s", errMsg)
}

// Close is an alias for Kill to maintain backward compatibility
func (i *Instance) Close() error {
	if !i.started {
		return fmt.Errorf("cannot close instance that has not been started")
	}
	return i.Kill()
}

func (i *Instance) Preview() (string, error) {
	if !i.started || i.Status == Paused {
		return "", nil
	}
	return i.tmuxSession.CapturePaneContent()
}

func (i *Instance) HasUpdated() (updated bool, hasPrompt bool) {
	if !i.started {
		return false, false
	}
	return i.tmuxSession.HasUpdated()
}

// TapEnter sends an enter key press to the tmux session if AutoYes is enabled.
func (i *Instance) TapEnter() {
	if !i.started || !i.AutoYes {
		return
	}
	if err := i.tmuxSession.TapEnter(); err != nil {
		log.ErrorLog.Printf("error tapping enter: %v", err)
	}
}

func (i *Instance) Attach() (chan struct{}, error) {
	if !i.started {
		return nil, fmt.Errorf("cannot attach instance that has not been started")
	}
	return i.tmuxSession.Attach()
}

func (i *Instance) SetPreviewSize(width, height int) error {
	if !i.started || i.Status == Paused {
		return fmt.Errorf("cannot set preview size for instance that has not been started or " +
			"is paused")
	}
	return i.tmuxSession.SetDetachedSize(width, height)
}

// GetGitWorktree returns the git worktree for the instance
func (i *Instance) GetGitWorktree() (*git.GitWorktree, error) {
	if !i.started {
		return nil, fmt.Errorf("cannot get git worktree for instance that has not been started")
	}
	return i.gitWorktree, nil
}

func (i *Instance) Started() bool {
	return i.started
}

// SetTitle sets the title of the instance. Returns an error if the instance has started.
// We cant change the title once it's been used for a tmux session etc.
func (i *Instance) SetTitle(title string) error {
	if i.started {
		return fmt.Errorf("cannot change title of a started instance")
	}
	i.Title = title
	return nil
}

func (i *Instance) Paused() bool {
	return i.Status == Paused
}

// TmuxAlive returns true if the tmux session is alive. This is a sanity check before attaching.
func (i *Instance) TmuxAlive() bool {
	return i.tmuxSession.DoesSessionExist()
}

// Pause stops the tmux session and removes the worktree, preserving the branch
func (i *Instance) Pause() error {
	if !i.started {
		return fmt.Errorf("cannot pause instance that has not been started")
	}
	if i.Status == Paused {
		return fmt.Errorf("instance is already paused")
	}

	var errs []error

	// Check if there are any changes to commit
	if dirty, err := i.gitWorktree.IsDirty(); err != nil {
		errs = append(errs, fmt.Errorf("failed to check if worktree is dirty: %w", err))
		log.ErrorLog.Print(err)
	} else if dirty {
		// Commit changes locally (without pushing to GitHub)
		commitMsg := fmt.Sprintf("[claudesquad] update from '%s' on %s (paused)", i.Title, time.Now().Format(time.RFC822))
		if err := i.gitWorktree.CommitChanges(commitMsg); err != nil {
			errs = append(errs, fmt.Errorf("failed to commit changes: %w", err))
			log.ErrorLog.Print(err)
			// Return early if we can't commit changes to avoid corrupted state
			return i.combineErrors(errs)
		}
	}

	// Close tmux session first since it's using the git worktree
	if err := i.tmuxSession.Close(); err != nil {
		errs = append(errs, fmt.Errorf("failed to close tmux session: %w", err))
		log.ErrorLog.Print(err)
		// Return early if we can't close tmux to avoid corrupted state
		return i.combineErrors(errs)
	}

	// Check if worktree exists before trying to remove it
	if _, err := os.Stat(i.gitWorktree.GetWorktreePath()); err == nil {
		// Remove worktree but keep branch
		if err := i.gitWorktree.Remove(); err != nil {
			errs = append(errs, fmt.Errorf("failed to remove git worktree: %w", err))
			log.ErrorLog.Print(err)
			return i.combineErrors(errs)
		}

		// Only prune if remove was successful
		if err := i.gitWorktree.Prune(); err != nil {
			errs = append(errs, fmt.Errorf("failed to prune git worktrees: %w", err))
			log.ErrorLog.Print(err)
			return i.combineErrors(errs)
		}
	}

	if err := i.combineErrors(errs); err != nil {
		log.ErrorLog.Print(err)
		return err
	}

	i.SetStatus(Paused)
	_ = clipboard.WriteAll(i.gitWorktree.GetBranchName())
	return nil
}

// Resume recreates the worktree and restarts the tmux session
func (i *Instance) Resume() error {
	if !i.started {
		return fmt.Errorf("cannot resume instance that has not been started")
	}
	if i.Status != Paused {
		return fmt.Errorf("can only resume paused instances")
	}

	// Check if branch is checked out
	if checked, err := i.gitWorktree.IsBranchCheckedOut(); err != nil {
		log.ErrorLog.Print(err)
		return fmt.Errorf("failed to check if branch is checked out: %w", err)
	} else if checked {
		return fmt.Errorf("cannot resume: branch is checked out, please switch to a different branch")
	}

	// Setup git worktree
	if err := i.gitWorktree.Setup(); err != nil {
		log.ErrorLog.Print(err)
		return fmt.Errorf("failed to setup git worktree: %w", err)
	}

	// Create new tmux session
	if err := i.tmuxSession.Start(i.gitWorktree.GetWorktreePath()); err != nil {
		log.ErrorLog.Print(err)
		// Cleanup git worktree if tmux session creation fails
		if cleanupErr := i.gitWorktree.Cleanup(); cleanupErr != nil {
			err = fmt.Errorf("%v (cleanup error: %v)", err, cleanupErr)
			log.ErrorLog.Print(err)
		}
		return fmt.Errorf("failed to start new session: %w", err)
	}

	i.SetStatus(Running)
	return nil
}

// UpdateDiffStats updates the git diff statistics for this instance
func (i *Instance) UpdateDiffStats() error {
	if !i.started {
		i.diffStats = nil
		return nil
	}

	if i.Status == Paused {
		// Keep the previous diff stats if the instance is paused
		return nil
	}

	stats := i.gitWorktree.Diff()
	if stats.Error != nil {
		if strings.Contains(stats.Error.Error(), "base commit SHA not set") {
			// Worktree is not fully set up yet, not an error
			i.diffStats = nil
			return nil
		}
		return fmt.Errorf("failed to get diff stats: %w", stats.Error)
	}

	i.diffStats = stats
	return nil
}

// GetDiffStats returns the current git diff statistics
func (i *Instance) GetDiffStats() *git.DiffStats {
	return i.diffStats
}

// prepareClaudeConversations creates the Claude directory and copies conversations before Claude starts
func prepareClaudeConversations(sourceProjectPath, targetProjectPath string) error {
	// Get the source Claude directory (simple conversion for regular projects)
	sourceClaudePath := filepath.Join(os.Getenv("HOME"), ".claude", "projects", 
		"-" + strings.ReplaceAll(sourceProjectPath, "/", "-")[1:])
	
	// Check if source directory exists
	if _, err := os.Stat(sourceClaudePath); os.IsNotExist(err) {
		log.InfoLog.Printf("No Claude conversations found at: %s", sourceClaudePath)
		return nil
	}
	
	// Create the target Claude directory path (complex conversion for worktrees)
	targetClaudePath := getClaudeProjectPath(targetProjectPath)
	
	log.InfoLog.Printf("Copying conversations:")
	log.InfoLog.Printf("  From: %s", sourceClaudePath)
	log.InfoLog.Printf("  To:   %s", targetClaudePath)
	
	// Create the directory
	if err := os.MkdirAll(targetClaudePath, 0755); err != nil {
		return fmt.Errorf("failed to create target Claude directory: %w", err)
	}
	
	// Copy conversation files
	sourceFiles, err := os.ReadDir(sourceClaudePath)
	if err != nil {
		return fmt.Errorf("failed to read source directory: %w", err)
	}
	
	copiedCount := 0
	for _, file := range sourceFiles {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".jsonl") {
			sourcePath := filepath.Join(sourceClaudePath, file.Name())
			targetPath := filepath.Join(targetClaudePath, file.Name())
			
			// Use the new function that updates cwd paths
			if err := copyAndUpdateConversation(sourcePath, targetPath, sourceProjectPath, targetProjectPath); err != nil {
				log.ErrorLog.Printf("Failed to copy %s: %v", file.Name(), err)
				continue
			}
			copiedCount++
		}
	}
	
	log.InfoLog.Printf("Copied %d conversations to %s (with updated cwd paths)", copiedCount, targetClaudePath)
	return nil
}

// copyClaudeConversationsToWorktree copies conversations to the Claude directory for the worktree
func copyClaudeConversationsToWorktree(sourceProjectPath, targetProjectPath string) error {
	// Get the source Claude directory
	sourceClaudePath := getClaudeProjectPath(sourceProjectPath)
	
	// Check if source directory exists
	if _, err := os.Stat(sourceClaudePath); os.IsNotExist(err) {
		log.InfoLog.Printf("No Claude conversations found for source project: %s", sourceProjectPath)
		return nil
	}
	
	// Find all possible Claude directories for the worktree
	homeDir, _ := os.UserHomeDir()
	claudeProjectsDir := filepath.Join(homeDir, ".claude", "projects")
	
	// List all directories to find the one Claude created for this worktree
	entries, err := os.ReadDir(claudeProjectsDir)
	if err != nil {
		return fmt.Errorf("failed to read Claude projects directory: %w", err)
	}
	
	// Find directories that contain the worktree path
	// Claude replaces underscores with dashes, so we need to check both
	worktreeBasename := filepath.Base(targetProjectPath)
	worktreeBasenameDashed := strings.ReplaceAll(worktreeBasename, "_", "-")
	
	for _, entry := range entries {
		if entry.IsDir() && (strings.Contains(entry.Name(), worktreeBasename) || 
			strings.Contains(entry.Name(), worktreeBasenameDashed)) {
			targetClaudePath := filepath.Join(claudeProjectsDir, entry.Name())
			log.InfoLog.Printf("Found Claude directory for worktree: %s", targetClaudePath)
			
			// Copy conversation files
			sourceFiles, err := os.ReadDir(sourceClaudePath)
			if err != nil {
				log.ErrorLog.Printf("Failed to read source directory %s: %v", sourceClaudePath, err)
				continue
			}
			
			for _, file := range sourceFiles {
				if !file.IsDir() && strings.HasSuffix(file.Name(), ".jsonl") {
					sourcePath := filepath.Join(sourceClaudePath, file.Name())
					targetPath := filepath.Join(targetClaudePath, file.Name())
					
					if err := copyFile(sourcePath, targetPath); err != nil {
						log.ErrorLog.Printf("Failed to copy %s: %v", file.Name(), err)
						continue
					}
					log.InfoLog.Printf("Copied conversation: %s", file.Name())
				}
			}
			
			return nil
		}
	}
	
	log.WarningLog.Printf("Could not find Claude directory for worktree %s", worktreeBasename)
	return fmt.Errorf("Claude directory not found for worktree")
}

// copyClaudeConversations copies Claude conversation files from source to target project
func copyClaudeConversations(sourceProjectPath, targetProjectPath string) error {
	// Convert paths to Claude's format
	sourceClaudePath := getClaudeProjectPath(sourceProjectPath)
	targetClaudePath := getClaudeProjectPath(targetProjectPath)
	
	log.InfoLog.Printf("Source Claude path: %s", sourceClaudePath)
	log.InfoLog.Printf("Target Claude path: %s", targetClaudePath)
	
	// Check if source directory exists
	if _, err := os.Stat(sourceClaudePath); os.IsNotExist(err) {
		log.InfoLog.Printf("No Claude conversations found for source project: %s", sourceProjectPath)
		return nil
	}
	
	// Create target directory if it doesn't exist
	if err := os.MkdirAll(targetClaudePath, 0755); err != nil {
		return fmt.Errorf("failed to create target Claude directory: %w", err)
	}
	
	// Read all files from source directory
	entries, err := os.ReadDir(sourceClaudePath)
	if err != nil {
		return fmt.Errorf("failed to read source Claude directory: %w", err)
	}
	
	// Copy each .jsonl file
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			sourcePath := filepath.Join(sourceClaudePath, entry.Name())
			targetPath := filepath.Join(targetClaudePath, entry.Name())
			
			if err := copyFile(sourcePath, targetPath); err != nil {
				log.ErrorLog.Printf("Failed to copy conversation %s: %v", entry.Name(), err)
				continue
			}
			log.InfoLog.Printf("Copied conversation: %s", entry.Name())
		}
	}
	
	return nil
}

// getClaudeProjectPath converts a project path to Claude's storage format
func getClaudeProjectPath(projectPath string) string {
	// Convert absolute path to Claude's format
	// Claude replaces ALL special characters with dashes, including dots and underscores
	cleanPath := projectPath
	
	// Replace forward slashes with dashes
	cleanPath = strings.ReplaceAll(cleanPath, "/", "-")
	
	// Replace dots with dashes (e.g., .claude-squad becomes -claude-squad)
	cleanPath = strings.ReplaceAll(cleanPath, ".", "-")
	
	// Replace underscores with dashes in the final component
	parts := strings.Split(cleanPath, "-")
	if len(parts) > 0 {
		parts[len(parts)-1] = strings.ReplaceAll(parts[len(parts)-1], "_", "-")
	}
	cleanPath = strings.Join(parts, "-")
	
	// Ensure we start with a dash
	if !strings.HasPrefix(cleanPath, "-") {
		cleanPath = "-" + cleanPath
	}
	
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".claude", "projects", cleanPath)
}

// copyFile copies a file from source to destination
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()
	
	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()
	
	_, err = io.Copy(destFile, sourceFile)
	return err
}

// copyAndUpdateConversation copies a conversation file and updates cwd paths
func copyAndUpdateConversation(src, dst, oldCwd, newCwd string) error {
	// Read the source file
	content, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("failed to read source file: %w", err)
	}
	
	// Replace all occurrences of the old cwd with the new cwd
	updatedContent := strings.ReplaceAll(string(content), 
		fmt.Sprintf(`"cwd":"%s"`, oldCwd), 
		fmt.Sprintf(`"cwd":"%s"`, newCwd))
	
	// Write to destination
	if err := os.WriteFile(dst, []byte(updatedContent), 0644); err != nil {
		return fmt.Errorf("failed to write destination file: %w", err)
	}
	
	return nil
}

// SendPrompt sends a prompt to the tmux session
func (i *Instance) SendPrompt(prompt string) error {
	if !i.started {
		return fmt.Errorf("instance not started")
	}
	if i.tmuxSession == nil {
		return fmt.Errorf("tmux session not initialized")
	}
	if err := i.tmuxSession.SendKeys(prompt); err != nil {
		return fmt.Errorf("error sending keys to tmux session: %w", err)
	}

	// Brief pause to prevent carriage return from being interpreted as newline
	time.Sleep(100 * time.Millisecond)
	if err := i.tmuxSession.TapEnter(); err != nil {
		return fmt.Errorf("error tapping enter: %w", err)
	}

	return nil
}
