package version

import (
	"runtime/debug"
	"testing"
)

func TestParseBuildInfo(t *testing.T) {
	tests := []struct {
		name         string
		settings     []debug.BuildSetting
		wantCommit   string
		wantTime     string
		wantDirty    bool
	}{
		{
			name: "full vcs info clean",
			settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "abcdef1234567890"},
				{Key: "vcs.time", Value: "2026-09-08T12:00:00Z"},
				{Key: "vcs.modified", Value: "false"},
			},
			wantCommit: "abcdef1234567890",
			wantTime:   "2026-09-08T12:00:00Z",
			wantDirty:  false,
		},
		{
			name: "full vcs info dirty",
			settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "1234567890abcdef"},
				{Key: "vcs.time", Value: "2026-09-08T13:00:00Z"},
				{Key: "vcs.modified", Value: "true"},
			},
			wantCommit: "1234567890abcdef",
			wantTime:   "2026-09-08T13:00:00Z",
			wantDirty:  true,
		},
		{
			name:       "empty settings defaults to dev",
			settings:   []debug.BuildSetting{},
			wantCommit: "dev",
			wantTime:   "",
			wantDirty:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			commit, commitTime, dirty := parseBuildInfo(tc.settings)
			if commit != tc.wantCommit {
				t.Errorf("commit = %q, want %q", commit, tc.wantCommit)
			}
			if commitTime != tc.wantTime {
				t.Errorf("commitTime = %q, want %q", commitTime, tc.wantTime)
			}
			if dirty != tc.wantDirty {
				t.Errorf("dirty = %v, want %v", dirty, tc.wantDirty)
			}
		})
	}
}

func TestVersion_GetGitCommit(t *testing.T) {
	commit := GetGitCommit()
	if commit == "" {
		t.Errorf("Expected non-empty git commit, got empty string")
	}

	_ = GetCommitTime()
	_ = IsDirty()
}
