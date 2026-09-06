package chat

// getGitBranch returns the current git branch name.
func (a *App) getGitBranch() string {
	if a.gitHelper == nil {
		return ""
	}
	branch, _ := a.gitHelper.CurrentBranch()
	return branch
}
