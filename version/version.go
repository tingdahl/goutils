package version

import (
	"runtime/debug"
	"sync"
)

var (
	buildInfoOnce sync.Once
	cachedCommit  string
	cachedTime    string
	cachedDirty   bool
)

func parseBuildInfo(settings []debug.BuildSetting) (commit, commitTime string, dirty bool) {
	for _, setting := range settings {
		switch setting.Key {
		case "vcs.revision":
			commit = setting.Value
		case "vcs.time":
			commitTime = setting.Value
		case "vcs.modified":
			dirty = setting.Value == "true"
		}
	}
	if commit == "" {
		commit = "dev"
	}
	return
}

func initBuildInfo() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		cachedCommit = "dev"
		return
	}
	cachedCommit, cachedTime, cachedDirty = parseBuildInfo(info.Settings)
}

// GetGitCommit returns the Git commit hash embedded at build time, or "dev" if not available.
func GetGitCommit() string {
	buildInfoOnce.Do(initBuildInfo)
	return cachedCommit
}

// GetCommitTime returns the commit time (RFC3339) embedded at build time.
func GetCommitTime() string {
	buildInfoOnce.Do(initBuildInfo)
	return cachedTime
}

// IsDirty returns true if the binary was built from a dirty working tree.
func IsDirty() bool {
	buildInfoOnce.Do(initBuildInfo)
	return cachedDirty
}
