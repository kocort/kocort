//go:build !llamacpp

package localmodel

func detectModelThinkingDefault(string) (bool, bool) {
	return false, false
}