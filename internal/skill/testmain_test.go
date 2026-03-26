package skill

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	code := m.Run()
	ResetSkillsWatchersForTests()
	os.Exit(code)
}
