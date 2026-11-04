package theme

// PatternByID 按 id 查一张图案。
func PatternByID(id string) (Pattern, bool) {
	for _, p := range patterns {
		if p.ID == id {
			return p, true
		}
	}
	return Pattern{}, false
}
