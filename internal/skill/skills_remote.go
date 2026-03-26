// Canonical implementation — migrated from runtime/skills_remote.go.
package skill

import (
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/kocort/kocort/internal/core"
)

var (
	remoteSkillsMu      sync.Mutex
	remoteSkillsCurrent *core.SkillRemoteEligibility
)

func setRemoteSkillEligibility(remote *core.SkillRemoteEligibility) {
	remoteSkillsMu.Lock()
	defer remoteSkillsMu.Unlock()
	remoteSkillsCurrent = remote
}

func GetRemoteSkillEligibility() *core.SkillRemoteEligibility {
	remoteSkillsMu.Lock()
	defer remoteSkillsMu.Unlock()
	return remoteSkillsCurrent
}

func DetectLocalSkillEligibilityNote() *core.SkillRemoteEligibility {
	return &core.SkillRemoteEligibility{
		Platforms: []string{runtime.GOOS},
		HasBin: func(bin string) bool {
			_, err := exec.LookPath(strings.TrimSpace(bin))
			return err == nil
		},
		HasAnyBin: func(bins []string) bool {
			for _, bin := range bins {
				if _, err := exec.LookPath(strings.TrimSpace(bin)); err == nil {
					return true
				}
			}
			return false
		},
	}
}
