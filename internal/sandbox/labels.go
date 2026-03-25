package sandbox

// MatchesLabels returns true if all filter labels are present and equal in target.
func MatchesLabels(target, filter map[string]string) bool {
	for k, v := range filter {
		if target[k] != v {
			return false
		}
	}
	return true
}
